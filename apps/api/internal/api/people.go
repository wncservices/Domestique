package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/auth0mgmt"
)

// PeopleConnector is the seam against Auth0's Management API — tests must
// not depend on a real tenant, the same reasoning KomootConnector and
// GarminConnector already carry. *auth0mgmt.Client satisfies this directly;
// no wrapper is needed the way LiveGarmin needs one, since none of these
// methods need translating into a different shape first.
type PeopleConnector interface {
	ListPeople(gateRole string, permissionRoles ...string) ([]auth0mgmt.Person, error)
	Invite(email, name string, roleNames []string) (auth0mgmt.Person, error)
	SetRoles(userID string, roleNames []string) error
	SendInviteEmail(email string) error
}

type personDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	// Role is the resolved app-level label ("admin", "rider", "viewer") —
	// the same computation Identify runs at sign-in (auth.ResolveRole), not
	// the raw Auth0 role names, so this page shows exactly what a person
	// can actually do rather than what happens to be assigned.
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt,omitempty"`
	LastLogin string `json:"lastLogin,omitempty"`
}

// peopleAvailable reports whether the deployment has Management API
// credentials at all — same shape as Komoot/Garmin's own optional
// credentials: nil degrades the page, it does not 500 the request.
func (s *Server) peopleAvailable(w http.ResponseWriter) bool {
	if s.People != nil {
		return true
	}
	writeJSON(w, http.StatusPreconditionFailed, map[string]string{
		"error": "this deployment has no Auth0 Management API access configured",
	})
	return false
}

// permissionRoleNames names the two Auth0 roles this page offers a choice
// between, in the order role names below expects (admin first, then
// rider) — auth.Config's own Roles mapping, not hardcoded, so this never
// drifts from what Identify actually resolves at sign-in.
func (s *Server) permissionRoleNames() (admin, rider string) {
	roles := s.Auth.Roles()
	if len(roles.Admin) > 0 {
		admin = roles.Admin[0]
	}
	if len(roles.Rider) > 0 {
		rider = roles.Rider[0]
	}
	return admin, rider
}

// roleNamesFor turns an app-level role label into the Auth0 role names a
// person holding it should have: always the gate role (RequiredGroup —
// "allowed in at all"), plus the permission role for anything above
// viewer. "viewer" is gate-only on purpose: there is no Auth0 role for it
// in this deployment (see roles.tf), it is simply the absence of the other
// two, the same fallback default_role already means at sign-in.
func (s *Server) roleNamesFor(label string) ([]string, error) {
	gate := s.Auth.RequiredGroup()
	if gate == "" {
		return nil, fmt.Errorf("this deployment has no auth.required_group configured — nothing to grant")
	}
	adminRole, riderRole := s.permissionRoleNames()

	switch label {
	case "admin":
		if adminRole == "" {
			return nil, fmt.Errorf("no admin role configured (auth.roles.admin)")
		}
		return []string{gate, adminRole}, nil
	case "rider":
		if riderRole == "" {
			return nil, fmt.Errorf("no rider role configured (auth.roles.rider)")
		}
		return []string{gate, riderRole}, nil
	case "viewer":
		return []string{gate}, nil
	default:
		return nil, fmt.Errorf("unknown role %q (want admin, rider or viewer)", label)
	}
}

func (s *Server) personDTO(p auth0mgmt.Person) personDTO {
	role := s.Auth.ResolveRole(p.Roles)
	dto := personDTO{ID: p.UserID, Email: p.Email, Name: p.Name, Role: roleLabel(role)}
	if !p.CreatedAt.IsZero() {
		dto.CreatedAt = formatTime(p.CreatedAt)
	}
	if !p.LastLogin.IsZero() {
		dto.LastLogin = formatTime(p.LastLogin)
	}
	return dto
}

// handlePeopleList lists everyone with access to this deployment — every
// gate-role member, each with the role they actually resolve to.
func (s *Server) handlePeopleList(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermManagePeople) {
		return
	}
	if !s.peopleAvailable(w) {
		return
	}

	gate := s.Auth.RequiredGroup()
	if gate == "" {
		writeJSON(w, http.StatusOK, []personDTO{})
		return
	}
	adminRole, riderRole := s.permissionRoleNames()
	var permissionRoles []string
	if adminRole != "" {
		permissionRoles = append(permissionRoles, adminRole)
	}
	if riderRole != "" {
		permissionRoles = append(permissionRoles, riderRole)
	}

	people, err := s.People.ListPeople(gate, permissionRoles...)
	if err != nil {
		s.logger().Warn("listing people failed", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "Auth0 would not list who has access just now.",
		})
		return
	}

	out := make([]personDTO, 0, len(people))
	for _, p := range people {
		out = append(out, s.personDTO(p))
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePeopleInvite creates a new Auth0 account, grants it the requested
// role, and sends the invite email — three steps against two different
// APIs (see auth0mgmt's own package doc), surfaced here as one request
// since a caller has no reasonable use for succeeding at only part of it.
func (s *Server) handlePeopleInvite(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermManagePeople) {
		return
	}
	if !s.peopleAvailable(w) {
		return
	}

	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	body.Name = strings.TrimSpace(body.Name)
	if body.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
		return
	}
	if body.Name == "" {
		body.Name = body.Email
	}

	roleNames, err := s.roleNamesFor(body.Role)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	person, err := s.People.Invite(body.Email, body.Name, roleNames)
	if err != nil {
		s.logger().Warn("inviting a person failed", "email", body.Email, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	// The account and its access both exist at this point — a failure past
	// here means the invite email needs resending, not starting over.
	if err := s.People.SendInviteEmail(body.Email); err != nil {
		s.logger().Warn("sending the invite email failed", "email", body.Email, "err", err)
		writeJSON(w, http.StatusOK, map[string]any{
			"person": s.personDTO(person),
			"error":  "The account was created, but the invite email could not be sent: " + err.Error(),
		})
		return
	}

	s.logger().Info("person invited", "email", body.Email, "role", body.Role, "by", auth.FromContext(r.Context()).User)
	writeJSON(w, http.StatusCreated, map[string]any{"person": s.personDTO(person)})
}

// handlePeopleSetRole changes which role a person holds — grant/revoke
// against Auth0, computed as a diff by auth0mgmt.SetRoles itself, not
// something this handler works out by hand.
func (s *Server) handlePeopleSetRole(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermManagePeople) {
		return
	}
	if !s.peopleAvailable(w) {
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no person id"})
		return
	}

	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	roleNames, err := s.roleNamesFor(body.Role)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := s.People.SetRoles(id, roleNames); err != nil {
		s.logger().Warn("changing a person's role failed", "id", id, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	s.logger().Info("person role changed", "id", id, "role", body.Role, "by", auth.FromContext(r.Context()).User)
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
