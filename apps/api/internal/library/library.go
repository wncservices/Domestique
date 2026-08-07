// Package library reads the route library — the git-tracked directory that is
// the source of truth.
//
// Layout:
//
//	routes/
//	  library.yaml               # accounts + default targets
//	  wilant/tour-of-flanders/
//	    route.gpx
//	    route.yaml               # name, description, targets, tags
package library

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wncservices/domestique/apps/api/internal/model"
)

const (
	LibraryFile = "library.yaml"
	RouteFile   = "route.yaml"
	GPXFile     = "route.gpx"
)

// Config is the library-wide library.yaml.
type Config struct {
	Accounts       []model.Account `yaml:"accounts"`
	DefaultTargets []string        `yaml:"default_targets"`
}

// Library is a loaded route library.
type Library struct {
	Root   string
	Config Config
	Routes []model.Route
}

// Account looks up a configured account by id.
func (l Library) Account(id string) (model.Account, error) {
	for _, a := range l.Config.Accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return model.Account{}, fmt.Errorf("unknown account %q", id)
}

// TargetsFor returns the accounts a route should be pushed to.
func (l Library) TargetsFor(r model.Route) []string {
	if r.Meta.Targets != nil {
		return *r.Meta.Targets
	}
	return l.Config.DefaultTargets
}

// Load reads a library from disk. It returns the library plus a list of
// non-fatal problems: one broken route must not block syncing the rest, since a
// bad file usually means a half-finished export and the other rider should not
// be held up by it.
func Load(root string) (*Library, []string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}

	cfg, err := loadConfig(abs)
	if err != nil {
		return nil, nil, err
	}

	known := make(map[string]bool, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		known[a.ID] = true
	}

	lib := &Library{Root: abs, Config: cfg}
	var problems []string
	var dirs []string

	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == GPXFile {
			dirs = append(dirs, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		route, err := loadRoute(dir, abs)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		targets := cfg.DefaultTargets
		if route.Meta.Targets != nil {
			targets = *route.Meta.Targets
		}
		for _, t := range targets {
			if !known[t] {
				problems = append(problems, fmt.Sprintf("%s: unknown target %q", route.Slug, t))
			}
		}
		if !route.Meta.IsEnabled() {
			continue
		}
		lib.Routes = append(lib.Routes, route)
	}

	return lib, problems, nil
}

func loadConfig(root string) (Config, error) {
	path := filepath.Join(root, LibraryFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("missing %s — is %s a route library?", path, root)
		}
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("%s: invalid YAML: %w", path, err)
	}
	return cfg, nil
}

func loadRoute(dir, root string) (model.Route, error) {
	gpxPath := filepath.Join(dir, GPXFile)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return model.Route{}, err
	}
	slug := filepath.ToSlash(rel)

	meta, err := loadMeta(dir)
	if err != nil {
		return model.Route{}, err
	}

	points, err := ReadPoints(gpxPath)
	if err != nil {
		return model.Route{}, err
	}

	return model.Route{
		Slug:        slug,
		Dir:         dir,
		GPXPath:     gpxPath,
		Meta:        meta,
		Stats:       ComputeStats(points),
		ContentHash: ContentHash(points, meta.Name, meta.Description),
	}, nil
}

func loadMeta(dir string) (model.RouteMeta, error) {
	path := filepath.Join(dir, RouteFile)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// A bare GPX drop is valid — name it after the directory.
		return model.RouteMeta{Name: titleize(filepath.Base(dir))}, nil
	}
	if err != nil {
		return model.RouteMeta{}, err
	}

	var meta model.RouteMeta
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return model.RouteMeta{}, fmt.Errorf("%s: invalid YAML: %w", path, err)
	}
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = titleize(filepath.Base(dir))
	}
	return meta, nil
}

func titleize(slug string) string {
	words := strings.Split(strings.ReplaceAll(slug, "_", "-"), "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
