package api

import (
	"github.com/wncservices/domestique/apps/api/internal/garmin"
)

// GarminConsumer is the OAuth1 consumer pair a sign-in is signed with.
//
// One pair per deployment, not per rider: it identifies the *application* to
// Garmin, and every rider's sign-in is signed with the same one. Where it
// comes from is the Server's business (see garminConsumer); the connector is
// handed one and uses it.
type GarminConsumer struct {
	Key    string
	Secret string
}

// Configured reports whether both halves are present. One without the other
// signs nothing, so it counts as absent.
func (c GarminConsumer) Configured() bool { return c.Key != "" && c.Secret != "" }

// GarminConnector signs riders in to Garmin Connect.
//
// An interface for the same reason KomootConnector is one: it is the seam
// against a third-party service that has no contract, and tests must not
// depend on Garmin being reachable or on anyone's real account.
type GarminConnector interface {
	// Connect signs in with a password. The password is used here and nowhere
	// else — what comes back is a session to store in its place.
	Connect(consumer GarminConsumer, email, password string) (garmin.Session, error)
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

// Connect signs in and returns the session to keep in the password's place.
//
// The profile lookup afterwards is deliberately not fatal. It is what puts a
// name on the connection, and it is also the first call that proves the
// OAuth1 token can be exchanged for a bearer — but it is an undocumented
// endpoint, and refusing a sign-in that otherwise worked because Garmin moved
// a profile URL would be the wrong trade. The connection is kept; the name is
// the email until the rider reconnects.
func (l LiveGarmin) Connect(consumer GarminConsumer, email, password string) (garmin.Session, error) {
	if !consumer.Configured() {
		return garmin.Session{}, garmin.ErrNoConsumer
	}

	client := garmin.New()
	// Supplied rather than read from the environment by the client, so the
	// pair an admin pasted into the UI is the one that signs the request.
	client.SetConsumer(consumer.Key, consumer.Secret)

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
