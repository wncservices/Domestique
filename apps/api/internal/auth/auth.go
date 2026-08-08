// Package auth identifies the rider making a request.
//
// Domestique does not authenticate anyone itself. It sits behind a reverse
// proxy that does — Traefik with an Authelia forwardAuth middleware — and
// Authelia hands the identity down as response headers the proxy copies onto
// the request: Remote-User, Remote-Name, Remote-Email, Remote-Groups.
//
// The whole scheme rests on one assumption: those headers came from the proxy
// and not from the client. A browser can set "Remote-User: someone-else" just
// as easily as the proxy can. So header trust is **opt-in** — Mode must be set
// to "proxy" explicitly, and the deployment must guarantee the app is not
// reachable except through the proxy. Running Mode "proxy" on a directly
// reachable port is the same as having no authentication at all.
package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Mode selects how a request is identified.
type Mode string

const (
	// ModeNone runs without authentication. Every request is treated as the
	// local admin (see LocalIdentity) and nothing is gated — running without a
	// proxy means running on your own machine. Right for a laptop, wrong for
	// anything reachable.
	ModeNone Mode = "none"
	// ModeProxy trusts Authelia's forwardAuth headers.
	ModeProxy Mode = "proxy"
)

// Header names Authelia sets and Traefik copies onto the upstream request.
const (
	HeaderUser   = "Remote-User"
	HeaderName   = "Remote-Name"
	HeaderEmail  = "Remote-Email"
	HeaderGroups = "Remote-Groups"
)

// Config is the auth section of domestique.yaml.
type Config struct {
	Mode Mode `yaml:"mode"`
	// TrustedProxies are CIDRs the forwarded headers may arrive from. Empty
	// means "trust any peer", which is only safe when the app has no route
	// from anywhere but the proxy — for example a ClusterIP service.
	TrustedProxies []string `yaml:"trusted_proxies,omitempty"`
	// RequiredGroup, when set, gates access on Authelia group membership.
	RequiredGroup string `yaml:"required_group,omitempty"`
	// Roles maps Authelia group names onto roles.
	Roles RoleMapping `yaml:"roles,omitempty"`
	// DefaultRole applies to an authenticated user whose groups match nothing
	// in Roles. Empty means "viewer" — read-only is the safe default for
	// someone Authelia let in but this app has no opinion about.
	DefaultRole string `yaml:"default_role,omitempty"`
}

// Identity is who is making a request, and what they may do.
type Identity struct {
	User   string   `json:"user"`
	Name   string   `json:"name,omitempty"`
	Email  string   `json:"email,omitempty"`
	Groups []string `json:"groups,omitempty"`
	Role   Role     `json:"role"`
}

// Anonymous reports whether nobody is identified.
func (i Identity) Anonymous() bool { return i.User == "" }

// DisplayName is the friendliest label available.
func (i Identity) DisplayName() string {
	if i.Name != "" {
		return i.Name
	}
	return i.User
}

// InGroup reports group membership.
func (i Identity) InGroup(group string) bool {
	for _, g := range i.Groups {
		if strings.EqualFold(g, group) {
			return true
		}
	}
	return false
}

// Authenticator turns requests into identities.
type Authenticator struct {
	mode          Mode
	requiredGroup string
	trusted       []*net.IPNet
	roles         RoleMapping
	defaultRole   Role
}

// New validates the config and builds an Authenticator.
func New(cfg Config) (*Authenticator, error) {
	a := &Authenticator{
		mode:          cfg.Mode,
		requiredGroup: cfg.RequiredGroup,
		roles:         cfg.Roles,
		defaultRole:   RoleViewer,
	}
	if a.mode == "" {
		a.mode = ModeNone
	}

	if cfg.DefaultRole != "" {
		role, err := parseRole(cfg.DefaultRole)
		if err != nil {
			return nil, fmt.Errorf("auth.default_role: %w", err)
		}
		a.defaultRole = role
	}

	switch a.mode {
	case ModeNone:
		if cfg.RequiredGroup != "" {
			return nil, fmt.Errorf("auth.required_group is set but auth.mode is %q: "+
				"nothing would enforce it", a.mode)
		}
	case ModeProxy:
	default:
		return nil, fmt.Errorf("unknown auth mode %q (want none or proxy)", a.mode)
	}

	for _, cidr := range cfg.TrustedProxies {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			// A bare IP is a reasonable thing to write; accept it as a /32.
			if ip := net.ParseIP(cidr); ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				network = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
			} else {
				return nil, fmt.Errorf("auth.trusted_proxies: %q is not a CIDR or IP", cidr)
			}
		}
		a.trusted = append(a.trusted, network)
	}

	return a, nil
}

// Mode reports the configured mode.
func (a *Authenticator) Mode() Mode { return a.mode }

// Enabled reports whether requests are authenticated at all.
func (a *Authenticator) Enabled() bool { return a.mode != ModeNone }

// Identify extracts the identity from a request.
//
// In ModeNone the headers are ignored entirely and everyone is the local
// admin. In ModeProxy the headers are read only
// when the peer is a trusted proxy; headers from anywhere else are discarded
// rather than trusted, because that is exactly what a spoofing attempt looks
// like.
func (a *Authenticator) Identify(r *http.Request) Identity {
	if a.mode == ModeNone {
		return LocalIdentity()
	}
	if a.mode != ModeProxy || !a.peerTrusted(r) {
		return Identity{}
	}

	user := strings.TrimSpace(r.Header.Get(HeaderUser))
	if user == "" {
		return Identity{}
	}

	groups := splitGroups(r.Header.Get(HeaderGroups))
	return Identity{
		User:   user,
		Name:   strings.TrimSpace(r.Header.Get(HeaderName)),
		Email:  strings.TrimSpace(r.Header.Get(HeaderEmail)),
		Groups: groups,
		Role:   a.resolveRole(groups),
	}
}

// LocalIdentity is who you are when authentication is off. Running without a
// proxy means running on your own machine, so it is an admin — anything less
// would make the app unusable in development for no security gain.
func LocalIdentity() Identity {
	return Identity{User: "local", Name: "Local user", Role: RoleAdmin}
}

// Authorize reports whether an identity may use the app, and why not if it
// may not.
func (a *Authenticator) Authorize(id Identity) error {
	if a.mode == ModeNone {
		return nil
	}
	if id.Anonymous() {
		return ErrUnauthenticated
	}
	if a.requiredGroup != "" && !id.InGroup(a.requiredGroup) {
		return fmt.Errorf("%w: not a member of %q", ErrForbidden, a.requiredGroup)
	}
	return nil
}

func (a *Authenticator) peerTrusted(r *http.Request) bool {
	if len(a.trusted) == 0 {
		return true
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range a.trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func splitGroups(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// Errors returned by Authorize.
var (
	ErrUnauthenticated = authError("not authenticated")
	ErrForbidden       = authError("forbidden")
)

type authError string

func (e authError) Error() string { return string(e) }

// context plumbing

type contextKey struct{}

// WithIdentity stores an identity on a request context.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext reads the identity a middleware stored.
func FromContext(ctx context.Context) Identity {
	if id, ok := ctx.Value(contextKey{}).(Identity); ok {
		return id
	}
	return Identity{}
}
