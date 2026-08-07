package source

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

const (
	routeFile = "route.yaml"
	gpxFile   = "route.gpx"
)

// FS reads a directory of GPX routes:
//
//	<root>/wilant/kemmelberg-loop/route.gpx
//	<root>/wilant/kemmelberg-loop/route.yaml   (optional)
//
// It is read-only. In a git-backed library, adding a route is a commit — which
// is the point: review, history and blame come for free.
type FS struct {
	Root string
}

// NewFS returns a source reading routes from a directory.
func NewFS(root string) (*FS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("route library %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("route library %s is not a directory", abs)
	}
	return &FS{Root: abs}, nil
}

func (f *FS) Describe() string { return "directory " + f.Root }

func (f *FS) List() ([]model.Route, []string, error) {
	var dirs []string
	err := filepath.WalkDir(f.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == gpxFile {
			dirs = append(dirs, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(dirs)

	var routes []model.Route
	var problems []string
	for _, dir := range dirs {
		route, err := f.loadRoute(dir)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if !route.IsEnabled() {
			continue
		}
		routes = append(routes, route)
	}
	return routes, problems, nil
}

func (f *FS) Track(slug string) ([]gpx.Point, error) {
	path, err := f.pathFor(slug)
	if err != nil {
		return nil, err
	}
	return gpx.ReadPoints(path)
}

func (f *FS) GPX(slug string) ([]byte, error) {
	path, err := f.pathFor(slug)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- pathFor refuses anything outside the library root.
	return os.ReadFile(path)
}

// pathFor resolves a slug to a GPX file, refusing anything that escapes the
// library root — slugs reach us straight from URLs.
func (f *FS) pathFor(slug string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(slug))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", ErrNotFound
	}

	path := filepath.Join(f.Root, clean, gpxFile)
	if !strings.HasPrefix(path, f.Root+string(os.PathSeparator)) {
		return "", ErrNotFound
	}
	if _, err := os.Stat(path); err != nil {
		return "", ErrNotFound
	}
	return path, nil
}

func (f *FS) loadRoute(dir string) (model.Route, error) {
	rel, err := filepath.Rel(f.Root, dir)
	if err != nil {
		return model.Route{}, err
	}
	slug := filepath.ToSlash(rel)

	meta, err := f.loadMeta(dir)
	if err != nil {
		return model.Route{}, err
	}

	gpxPath := filepath.Join(dir, gpxFile)
	points, err := gpx.ReadPoints(gpxPath)
	if err != nil {
		return model.Route{}, err
	}

	updated := ""
	if info, err := os.Stat(gpxPath); err == nil {
		updated = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	}

	return model.Route{
		RouteMeta:   meta,
		Slug:        slug,
		Stats:       gpx.ComputeStats(points),
		ContentHash: gpx.ContentHash(points, meta.Name, meta.Description),
		Origin:      filepath.ToSlash(rel),
		UpdatedAt:   updated,
	}, nil
}

func (f *FS) loadMeta(dir string) (model.RouteMeta, error) {
	path := filepath.Join(dir, routeFile)
	// #nosec G304 -- path is built from a directory walk rooted at the library.
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// A bare GPX drop is valid — name it after the directory.
		return model.RouteMeta{Name: Titleize(filepath.Base(dir))}, nil
	}
	if err != nil {
		return model.RouteMeta{}, err
	}

	var meta model.RouteMeta
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return model.RouteMeta{}, fmt.Errorf("%s: invalid YAML: %w", path, err)
	}
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = Titleize(filepath.Base(dir))
	}
	return meta, nil
}

// Titleize turns a slug into a display name: "kemmelberg-loop" -> "Kemmelberg Loop".
func Titleize(slug string) string {
	words := strings.Split(strings.ReplaceAll(slug, "_", "-"), "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
