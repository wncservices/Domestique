package api

import (
	"github.com/wncservices/domestique/apps/api/internal/garmin"
	"github.com/wncservices/domestique/apps/api/internal/targets"
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
	// Devices lists the head units on an account, from a stored session. No
	// password: this is what the session is for.
	Devices(consumer GarminConsumer, session garmin.Session) ([]garmin.Device, error)
	// Courses returns a client that can push and remove courses, from that
	// same stored session.
	Courses(consumer GarminConsumer, session garmin.Session) (targets.Courses, error)
	// ListCourses lists every course already on the account — sync-back and
	// duplicate detection, not scoped to anything this app itself pushed.
	ListCourses(consumer GarminConsumer, session garmin.Session) ([]garmin.Course, error)
	// DownloadGPX fetches one of those courses' tracks, to bring it into the
	// library as a new route.
	DownloadGPX(consumer GarminConsumer, session garmin.Session, courseID string) ([]byte, error)
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

// resume rebuilds a signed-in client from a stored session.
//
// Devices and Courses both need exactly this and nothing more. They arrived
// from separate branches doing it identically, which is the moment to put it
// in one place rather than keep two copies in step.
//
// The consumer is needed even though no password is: every call underneath
// begins by exchanging the OAuth1 token for a bearer, and that exchange is
// signed with it.
func (l LiveGarmin) resume(consumer GarminConsumer, session garmin.Session) (*garmin.Client, error) {
	if !consumer.Configured() {
		return nil, garmin.ErrNoConsumer
	}

	client := garmin.New()
	client.SetConsumer(consumer.Key, consumer.Secret)
	client.Resume(session)
	return client, nil
}

// Devices lists the head units registered to a connected account.
func (l LiveGarmin) Devices(consumer GarminConsumer, session garmin.Session) ([]garmin.Device, error) {
	client, err := l.resume(consumer, session)
	if err != nil {
		return nil, err
	}
	return client.Devices()
}

// Courses returns a client for pushing courses to a connected account.
func (l LiveGarmin) Courses(consumer GarminConsumer, session garmin.Session) (targets.Courses, error) {
	return l.resume(consumer, session)
}

// ListCourses lists every course already on the account.
func (l LiveGarmin) ListCourses(consumer GarminConsumer, session garmin.Session) ([]garmin.Course, error) {
	client, err := l.resume(consumer, session)
	if err != nil {
		return nil, err
	}
	return client.ListCourses()
}

// DownloadGPX fetches one course's track as GPX.
func (l LiveGarmin) DownloadGPX(consumer GarminConsumer, session garmin.Session, courseID string) ([]byte, error) {
	client, err := l.resume(consumer, session)
	if err != nil {
		return nil, err
	}
	return client.DownloadGPX(courseID)
}
