package garmin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Profile is the little Connect will say about whose account this is.
type Profile struct {
	DisplayName string `json:"displayName"`
	FullName    string `json:"fullName"`
}

// Name is what to show a rider: their own name if Connect gives one, and the
// opaque profile id only as a fallback.
func (p Profile) Name() string {
	if p.FullName != "" {
		return p.FullName
	}
	return p.DisplayName
}

// Profile fetches the signed-in account's own profile.
//
// Two jobs. It names the account in the UI, so a rider can see *which* Garmin
// they connected. More usefully it is the first call that actually exercises
// the OAuth2 exchange: signing in only proves the SSO ticket converted to an
// OAuth1 token, and a token that cannot be exchanged for a bearer would
// otherwise look like a successful connection until the first push.
//
// Undocumented like the rest, so callers treat a failure as "no name known"
// rather than "sign-in failed" — see api.LiveGarmin.
func (c *Client) Profile() (Profile, error) {
	bearer, err := c.bearerToken()
	if err != nil {
		return Profile{}, err
	}

	raw, status, err := c.do(http.MethodGet, c.APIBase+"/userprofile-service/socialProfile", nil, "",
		header{"Authorization", "Bearer " + bearer},
		header{"Accept", "application/json"},
		header{"X-Requested-With", "XMLHttpRequest"},
	)
	if err != nil {
		return Profile{}, err
	}
	if status != http.StatusOK {
		return Profile{}, fmt.Errorf("garmin: the profile request returned %d: %s", status, snippet(raw))
	}

	var profile Profile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return Profile{}, fmt.Errorf("garmin: unreadable profile response: %w", err)
	}
	if profile.DisplayName == "" && profile.FullName == "" {
		return Profile{}, errors.New("garmin: the profile response named nobody")
	}
	return profile, nil
}
