package auth

import "testing"

func TestRoleOrdering(t *testing.T) {
	if !RoleAdmin.AtLeast(RoleRider) || !RoleAdmin.AtLeast(RoleViewer) {
		t.Error("admin should outrank rider and viewer")
	}
	if !RoleRider.AtLeast(RoleViewer) {
		t.Error("rider should outrank viewer")
	}
	if RoleViewer.AtLeast(RoleRider) || RoleRider.AtLeast(RoleAdmin) {
		t.Error("ordering is inverted somewhere")
	}
	if RoleNone.AtLeast(RoleViewer) {
		t.Error("having no role should not satisfy viewer")
	}
}

func TestPermissionsPerRole(t *testing.T) {
	cases := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleViewer, PermReadRoutes, true},
		{RoleViewer, PermUploadRoute, false},
		{RoleViewer, PermPush, false},
		{RoleViewer, PermEditAny, false},

		{RoleRider, PermReadRoutes, true},
		{RoleRider, PermUploadRoute, true},
		{RoleRider, PermKomootSync, true},
		{RoleRider, PermPush, true},
		{RoleRider, PermEditOwn, true},
		{RoleRider, PermEditAny, false},
		{RoleRider, PermManageAccounts, true},
		{RoleViewer, PermManageAccounts, false},

		{RoleAdmin, PermEditAny, true},
		{RoleAdmin, PermPush, true},

		{RoleNone, PermReadRoutes, false},
	}

	for _, tc := range cases {
		if got := tc.role.Can(tc.perm); got != tc.want {
			t.Errorf("%s.Can(%s) = %v, want %v", tc.role, tc.perm, got, tc.want)
		}
	}
}

// An unknown permission must deny, not allow — otherwise a typo in a handler
// silently opens a hole.
func TestUnknownPermissionDenies(t *testing.T) {
	if RoleAdmin.Can(Permission("routes:obliterate")) {
		t.Error("unknown permission granted to admin")
	}
}

func TestRoleValidation(t *testing.T) {
	for _, r := range []Role{RoleViewer, RoleRider, RoleAdmin} {
		if !r.Valid() {
			t.Errorf("%q should be valid", r)
		}
	}
	if RoleNone.Valid() || Role("superuser").Valid() {
		t.Error("invalid roles reported as valid")
	}

	if _, err := parseRole("ADMIN"); err != nil {
		t.Errorf("role parsing should be case-insensitive: %v", err)
	}
	if _, err := parseRole("wizard"); err == nil {
		t.Error("unknown role accepted")
	}
}

func TestResolveRoleFromGroups(t *testing.T) {
	a := mustNew(t, Config{
		Mode: ModeProxy,
		Roles: RoleMapping{
			Admin:  []string{"domestique-admins"},
			Rider:  []string{"cyclists"},
			Viewer: []string{"guests"},
		},
	})

	cases := map[string]struct {
		groups []string
		want   Role
	}{
		"admin group":   {[]string{"domestique-admins"}, RoleAdmin},
		"rider group":   {[]string{"cyclists"}, RoleRider},
		"viewer group":  {[]string{"guests"}, RoleViewer},
		"case insensit": {[]string{"Cyclists"}, RoleRider},
		"no match":      {[]string{"random"}, RoleViewer}, // default
		// Most privileged match wins — what a config reader expects.
		"multiple": {[]string{"cyclists", "domestique-admins"}, RoleAdmin},
	}

	for name, tc := range cases {
		if got := a.resolveRole(tc.groups); got != tc.want {
			t.Errorf("%s: role = %q, want %q", name, got, tc.want)
		}
	}
}

func TestDefaultRoleIsConfigurable(t *testing.T) {
	a := mustNew(t, Config{Mode: ModeProxy, DefaultRole: "rider"})
	if got := a.resolveRole([]string{"nothing-mapped"}); got != RoleRider {
		t.Errorf("default role = %q, want rider", got)
	}

	// Unmapped users are read-only unless told otherwise.
	b := mustNew(t, Config{Mode: ModeProxy})
	if got := b.resolveRole(nil); got != RoleViewer {
		t.Errorf("implicit default = %q, want viewer", got)
	}

	if _, err := New(Config{Mode: ModeProxy, DefaultRole: "wizard"}); err == nil {
		t.Error("invalid default_role accepted")
	}
}

func TestIdentifyAssignsRole(t *testing.T) {
	a := mustNew(t, Config{
		Mode:  ModeProxy,
		Roles: RoleMapping{Admin: []string{"domestique-admins"}},
	})

	id := a.Identify(request("10.0.0.1:1000", map[string]string{
		HeaderUser:   "wilant",
		HeaderGroups: "domestique-admins",
	}))
	if id.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", id.Role)
	}
}

// Ownership: riders may change what they uploaded, admins anything.
func TestCanEditRoute(t *testing.T) {
	admin := Identity{User: "boss", Role: RoleAdmin}
	rider := Identity{User: "wilant", Role: RoleRider}
	viewer := Identity{User: "guest", Role: RoleViewer}

	if !admin.CanEditRoute("someone-else") {
		t.Error("admin cannot edit another rider's route")
	}
	if !rider.CanEditRoute("wilant") {
		t.Error("rider cannot edit their own route")
	}
	if rider.CanEditRoute("someone-else") {
		t.Error("rider can edit another rider's route")
	}
	// Case differences between Authelia and the stored owner must not lock
	// someone out of their own upload.
	if !rider.CanEditRoute("Wilant") {
		t.Error("owner match should be case-insensitive")
	}
	// A route with no recorded owner would otherwise be uneditable forever.
	if !rider.CanEditRoute("") {
		t.Error("rider cannot edit an unowned route")
	}
	if viewer.CanEditRoute("guest") {
		t.Error("viewer can edit routes")
	}
}

func TestPermissionsListing(t *testing.T) {
	if len(RoleViewer.Permissions()) != 1 {
		t.Errorf("viewer permissions = %v, want just read", RoleViewer.Permissions())
	}
	admin := RoleAdmin.Permissions()
	if len(admin) != 7 {
		t.Errorf("admin permissions = %v, want all seven", admin)
	}
}
