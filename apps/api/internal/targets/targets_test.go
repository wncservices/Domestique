package targets

import (
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/model"
)

func TestBuildReturnsTheRightAdapter(t *testing.T) {
	garmin, err := Build(model.Account{ID: "garmin:wilant", Provider: model.ProviderGarmin, Rider: "wilant"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := garmin.(*Garmin); !ok {
		t.Errorf("garmin account built a %T", garmin)
	}

	wahoo, err := Build(model.Account{ID: "wahoo:friend", Provider: model.ProviderWahoo, Rider: "friend"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := wahoo.(*Wahoo); !ok {
		t.Errorf("wahoo account built a %T", wahoo)
	}
}

func TestBuildRejectsUnknownProvider(t *testing.T) {
	if _, err := Build(model.Account{ID: "strava:x", Provider: "strava", Rider: "x"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

// An adapter with nothing wired to it must fail rather than silently claim
// success — a silent success would record state and the route would never be
// retried. Garmin is implemented now, but one built without a session is
// still in this position, and that is exactly what `domestique push` from a
// laptop does.
func TestStubsFailLoudly(t *testing.T) {
	for name, target := range map[string]Target{
		"garmin without a session": &Garmin{},
		"wahoo":                    &Wahoo{},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := target.Create(t.Context(), model.Route{}); err == nil {
				t.Error("Create returned no error")
			}
			if _, err := target.Update(t.Context(), "id", model.Route{}); err == nil {
				t.Error("Update returned no error")
			}
			if err := target.Delete(t.Context(), "id"); err == nil {
				t.Error("Delete returned no error")
			}
		})
	}
}

// Implemented drives the UI's "not wired up" badge. It must say what is true:
// Garmin pushes for real now, Wahoo does not.
func TestImplementedMatchesReality(t *testing.T) {
	if !Implemented(model.ProviderGarmin) {
		t.Error("garmin has a working adapter but reports unimplemented")
	}
	if Implemented(model.ProviderWahoo) {
		t.Error("wahoo still returns errors from every method — see wahoo.go")
	}
	if Implemented("strava") {
		t.Error("unknown provider reports implemented")
	}
}
