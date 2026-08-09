package api

import "github.com/wncservices/domestique/apps/api/internal/komoot"

// LiveKomoot is the real connector: it talks to Komoot.
//
// It lives here rather than in internal/komoot because it exists to satisfy
// this package's interface. internal/komoot stays a client with no opinion
// about how a session is stored.
type LiveKomoot struct{}

// Connect signs in and returns the session to keep in the password's place.
func (LiveKomoot) Connect(email, password string) (KomootImporter, KomootSession, error) {
	client := komoot.New()
	if err := client.Login(email, password); err != nil {
		return nil, KomootSession{}, err
	}

	userID, token := client.Session()
	return client, KomootSession{
		UserID:      userID,
		Token:       token,
		DisplayName: client.DisplayName(),
	}, nil
}

// Resume rebuilds a client from a stored session, without a password.
func (LiveKomoot) Resume(userID, token string) KomootImporter {
	client := komoot.New()
	client.LoginWithToken(userID, token)
	return client
}
