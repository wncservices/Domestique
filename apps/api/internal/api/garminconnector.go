package api

import (
	"github.com/wncservices/domestique/apps/api/internal/garmin"
)

// GarminConnector signs riders in to Garmin Connect.
//
// An interface for the same reason KomootConnector is one: it is the seam
// against a third-party service that has no contract, and tests must not
// depend on Garmin being reachable or on anyone's real account.
type GarminConnector interface {
	// Connect signs in with a password. The password is used here and nowhere
	// else — what comes back is a session to store in its place.
	Connect(email, password string) (garmin.Session, error)
	// Ready reports why sign-in cannot be offered, or nil when it can. The UI
	// asks before showing a form, so nobody types a password into something
	// that was never going to work.
	Ready() error
}

// LiveGarmin is the real connector: it talks to Garmin.
//
// It lives here rather than in internal/garmin because it exists to satisfy
// this package's interface. internal/garmin stays a client with no opinion
// about how a session is stored.
type LiveGarmin struct {
	// Log receives the one thing that is worth knowing and not worth failing
	// over: that the profile lookup did not work. Nil is fine.
	Log func(msg string, args ...any)
}

// Ready reports whether the OAuth1 consumer is configured.
func (LiveGarmin) Ready() error {
	if !garmin.HasConsumer() {
		return garmin.ErrNoConsumer
	}
	return nil
}

// Connect signs in and returns the session to keep in the password's place.
//
// The profile lookup afterwards is deliberately not fatal. It is what puts a
// name on the connection, and it is also the first call that proves the
// OAuth1 token can be exchanged for a bearer — but it is an undocumented
// endpoint, and refusing a sign-in that otherwise worked because Garmin moved
// a profile URL would be the wrong trade. The connection is kept; the name is
// the email until the rider reconnects.
func (l LiveGarmin) Connect(email, password string) (garmin.Session, error) {
	client := garmin.New()
	if err := client.Login(email, password); err != nil {
		return garmin.Session{}, err
	}

	session := client.Session()
	if profile, err := client.Profile(); err == nil {
		session.DisplayName = profile.Name()
	} else if l.Log != nil {
		l.Log("garmin profile lookup failed; the connection is kept without a name", "err", err)
	}
	return session, nil
}
