// Package model holds the core domain types.
//
// The library on disk is the desired state; the state store records what each
// remote account actually holds. Everything else is a diff between the two.
package model

import "fmt"

// Provider is a route host we can push to.
type Provider string

const (
	ProviderGarmin Provider = "garmin"
	ProviderWahoo  Provider = "wahoo"
)

// Account is one rider's account on one provider, e.g. "garmin:wilant".
type Account struct {
	ID       string   `yaml:"id"`
	Provider Provider `yaml:"provider"`
	Rider    string   `yaml:"rider"`
	Label    string   `yaml:"label,omitempty"`
}

// EnvPrefix is the env var prefix for this account's credentials, e.g. GARMIN_WILANT.
func (a Account) EnvPrefix() string {
	return sanitizeEnv(fmt.Sprintf("%s_%s", a.Provider, a.Rider))
}

// RouteMeta is a route's editable metadata — the contents of route.yaml in a
// filesystem library, or the metadata columns in a database one.
type RouteMeta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// Targets nil means "use the library default"; an empty list means "push nowhere".
	Targets *[]string `yaml:"targets,omitempty"`
	Tags    []string  `yaml:"tags,omitempty"`
	Enabled *bool     `yaml:"enabled,omitempty"`
}

// IsEnabled reports whether the route should be synced. Absent means yes.
func (m RouteMeta) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

// RouteStats is derived from the GPX track. Wahoo requires these at create time.
type RouteStats struct {
	DistanceM  float64
	AscentM    float64
	StartLat   float64
	StartLng   float64
	PointCount int
}

// Route is one route in the library: a GPX track plus its metadata and stats.
//
// It carries no file paths on purpose — a route may come from a directory or
// from a database row, and only its source knows which. Fetch the track itself
// through the source.
type Route struct {
	RouteMeta

	Slug        string
	Stats       RouteStats
	ContentHash string
	// Origin is a human-readable hint about where this route came from
	// (a path, or "database"). Display only.
	Origin string
	// UpdatedAt is when the route last changed in its source, if known.
	UpdatedAt string
}

// Op is the kind of change a plan item represents.
type Op string

const (
	OpCreate Op = "create"
	OpUpdate Op = "update"
	OpDelete Op = "delete"
	OpNoop   Op = "noop"
)

// PlanItem is one intended change against one account.
type PlanItem struct {
	Op        Op
	AccountID string
	Slug      string
	Reason    string
	RemoteID  string
	Route     *Route
}

// Plan is the full set of intended changes across all accounts.
type Plan struct {
	Items []PlanItem
}

// Changes returns everything that is not already up to date.
func (p Plan) Changes() []PlanItem {
	out := make([]PlanItem, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Op != OpNoop {
			out = append(out, item)
		}
	}
	return out
}

func sanitizeEnv(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			b[i] = c - 32
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		default:
			b[i] = '_'
		}
	}
	return string(b)
}
