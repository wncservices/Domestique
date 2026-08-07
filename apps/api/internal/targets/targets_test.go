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

// The stubs must fail rather than silently claim success — a silent success
// would record state and the route would never be retried once implemented.
func TestStubsFailLoudly(t *testing.T) {
	for name, target := range map[string]Target{
		"garmin": &Garmin{},
		"wahoo":  &Wahoo{},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := target.Create(model.Route{}); err == nil {
				t.Error("Create returned no error")
			}
			if _, err := target.Update("id", model.Route{}); err == nil {
				t.Error("Update returned no error")
			}
			if err := target.Delete("id"); err == nil {
				t.Error("Delete returned no error")
			}
		})
	}
}

// Implemented drives the UI's "not wired up" message and the disabled push
// button. It must stay false until an adapter genuinely works.
func TestImplementedMatchesReality(t *testing.T) {
	for _, provider := range []model.Provider{model.ProviderGarmin, model.ProviderWahoo} {
		if Implemented(provider) {
			t.Errorf("%s reports implemented, but its adapter still returns errors — "+
				"flip this only when the adapter works", provider)
		}
	}
	if Implemented("strava") {
		t.Error("unknown provider reports implemented")
	}
}
