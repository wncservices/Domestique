// Package state records what each remote account currently holds, so pushes
// are idempotent.
//
// A JSON file is deliberate: two riders and a few hundred routes do not need a
// database, and a single file on a PVC is one less thing to back up. The Store
// interface is the seam to swap in SQLite or Postgres if that ever changes.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Entry is one route as it exists on one remote account.
type Entry struct {
	AccountID   string `json:"account_id"`
	Slug        string `json:"slug"`
	RemoteID    string `json:"remote_id"`
	ContentHash string `json:"content_hash"`
	Name        string `json:"name,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

// Store is the persistence seam.
type Store interface {
	All() []Entry
	ForAccount(accountID string) map[string]Entry
	Record(e Entry) error
	Forget(accountID, slug string) error
}

type fileStore struct {
	path    string
	entries map[string]Entry // keyed by accountID + "\x00" + slug
}

// Open loads a state file, creating an empty one if it does not exist.
func Open(path string) (Store, error) {
	s := &fileStore{path: path, entries: map[string]Entry{}}

	// #nosec G304 -- the state path is operator configuration, not user input.
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}

	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("%s: corrupt state file: %w", path, err)
	}
	for _, e := range entries {
		s.entries[key(e.AccountID, e.Slug)] = e
	}
	return s, nil
}

func (s *fileStore) All() []Entry {
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AccountID != out[j].AccountID {
			return out[i].AccountID < out[j].AccountID
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

func (s *fileStore) ForAccount(accountID string) map[string]Entry {
	out := map[string]Entry{}
	for _, e := range s.entries {
		if e.AccountID == accountID {
			out[e.Slug] = e
		}
	}
	return out
}

func (s *fileStore) Record(e Entry) error {
	e.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.entries[key(e.AccountID, e.Slug)] = e
	return s.flush()
}

func (s *fileStore) Forget(accountID, slug string) error {
	delete(s.entries, key(accountID, slug))
	return s.flush()
}

// flush writes via a temp file and rename so a crash mid-write cannot leave a
// truncated state file behind — losing state means re-uploading every route.
func (s *fileStore) flush() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(s.All(), "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func key(accountID, slug string) string { return accountID + "\x00" + slug }
