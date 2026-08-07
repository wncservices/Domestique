// Command domestique reconciles a library of cycling routes into each rider's
// Garmin Connect and Wahoo account.
//
// Routes come from a source: a directory of GPX files (typically a checkout of
// a separate, private routes repo) or a database that accepts uploads. The app
// itself holds no route data.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/api"
	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
	"github.com/wncservices/domestique/apps/api/internal/sync"
	"github.com/wncservices/domestique/apps/api/internal/targets"
)

const usage = `domestique — fetch-and-carry for cycling routes

usage: domestique <command> [flags]

commands:
  validate   read the route source and report problems
  plan       show what would change on each account
  push       apply the plan (use --dry-run to preview)
  state      list what each account is recorded as holding
  import     copy a directory of GPX routes into a database source
  serve      run the HTTP API and the web UI

common flags:
  --config PATH    app config (default domestique.yaml)
  --state PATH     sync state file (default .domestique-state.json)

source flags (override the config file):
  --source KIND    fs or db
  --library PATH   route directory when --source=fs
  --db DSN         SQLite file when --source=db

serve flags:
  --addr ADDR      listen address (default :8080)
  --web-dir PATH   built frontend to serve (default apps/web/dist)

import flags:
  --from PATH      directory of GPX routes to import into the database
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	configPath := fs.String("config", "domestique.yaml", "app config file")
	statePath := fs.String("state", ".domestique-state.json", "sync state file")
	sourceKind := fs.String("source", "", "route source: fs or db")
	libPath := fs.String("library", "", "route directory when --source=fs")
	dsn := fs.String("db", "", "SQLite file when --source=db")
	dryRun := fs.Bool("dry-run", false, "print what push would do without doing it")
	addr := fs.String("addr", ":8080", "listen address for serve")
	webDir := fs.String("web-dir", filepath.Join("apps", "web", "dist"), "built frontend to serve")
	from := fs.String("from", "", "directory of GPX routes to import")

	switch cmd {
	case "validate", "plan", "push", "state", "serve", "import":
		if err := fs.Parse(rest); err != nil {
			return err
		}
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: domestique help)", cmd)
	}

	// `state` reads nothing but the state file, so it works even when the
	// source is unreachable — which is when you most want to inspect it.
	if cmd == "state" {
		return runState(*statePath)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	applyOverrides(cfg, *sourceKind, *libPath, *dsn)

	src, err := openSource(cfg)
	if err != nil {
		return err
	}
	if closer, ok := src.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	switch cmd {
	case "validate":
		return runValidate(src, cfg)
	case "plan":
		return runPlan(src, cfg, *statePath)
	case "push":
		return runPush(src, cfg, *statePath, *dryRun)
	case "import":
		return runImport(src, *from)
	case "serve":
		return runServe(src, cfg, *statePath, *addr, *webDir)
	}
	return nil
}

func applyOverrides(cfg *config.Config, kind, libPath, dsn string) {
	if kind != "" {
		cfg.Source.Kind = config.SourceKind(kind)
	}
	if libPath != "" {
		cfg.Source.Kind = config.SourceFS
		cfg.Source.Path = libPath
	}
	if dsn != "" {
		cfg.Source.Kind = config.SourceDB
		cfg.Source.DSN = dsn
	}
}

func openSource(cfg *config.Config) (source.Source, error) {
	switch cfg.Source.Kind {
	case config.SourceDB:
		if cfg.Source.DSN == "" {
			return nil, errors.New("source kind db needs a --db path (or source.dsn in the config)")
		}
		return source.OpenDB(cfg.Source.DSN)
	default:
		return source.NewFS(cfg.Source.Path)
	}
}

func runValidate(src source.Source, cfg *config.Config) error {
	routes, problems, err := src.List()
	if err != nil {
		return err
	}

	fmt.Println(src.Describe())
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROUTE\tNAME\tDISTANCE\tASCENT\tPOINTS\tTARGETS")
	for _, r := range routes {
		fmt.Fprintf(w, "%s\t%s\t%.1f km\t%.0f m\t%d\t%v\n",
			r.Slug, r.Name, r.Stats.DistanceM/1000, r.Stats.AscentM,
			r.Stats.PointCount, cfg.TargetsFor(r))
		for _, unknown := range cfg.UnknownTargets(r) {
			problems = append(problems, fmt.Sprintf("%s: unknown target %q", r.Slug, unknown))
		}
	}
	w.Flush()

	fmt.Printf("\n%d route(s), %d account(s)\n", len(routes), len(cfg.Accounts))
	return reportProblems(problems)
}

func runPlan(src source.Source, cfg *config.Config, statePath string) error {
	routes, problems, err := src.List()
	if err != nil {
		return err
	}
	store, err := state.Open(statePath)
	if err != nil {
		return err
	}

	printPlan(sync.BuildPlan(routes, cfg, store))
	return reportProblems(problems)
}

func runPush(src source.Source, cfg *config.Config, statePath string, dryRun bool) error {
	routes, problems, err := src.List()
	if err != nil {
		return err
	}
	store, err := state.Open(statePath)
	if err != nil {
		return err
	}

	plan := sync.BuildPlan(routes, cfg, store)
	printPlan(plan)

	if dryRun {
		fmt.Println("\ndry run — nothing pushed")
		return reportProblems(problems)
	}
	if len(plan.Changes()) == 0 {
		return reportProblems(problems)
	}

	byAccount := map[string]targets.Target{}
	for _, account := range cfg.Accounts {
		target, err := targets.Build(account)
		if err != nil {
			return err
		}
		byAccount[account.ID] = target
	}

	failures := sync.Apply(plan, store, byAccount)
	if err := reportProblems(problems); err != nil {
		return err
	}
	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr)
		for _, f := range failures {
			fmt.Fprintln(os.Stderr, "failed:", f)
		}
		return fmt.Errorf("%d of %d change(s) failed", len(failures), len(plan.Changes()))
	}

	fmt.Printf("\npushed %d change(s)\n", len(plan.Changes()))
	return nil
}

// runImport moves an existing directory library into a database one — the
// migration path for "we started with a repo, now we want uploads".
func runImport(dst source.Source, from string) error {
	writable, ok := source.AsWritable(dst)
	if !ok {
		return fmt.Errorf("%s is read-only; import needs --source=db", dst.Describe())
	}
	if from == "" {
		return errors.New("import needs --from <directory>")
	}

	src, err := source.NewFS(from)
	if err != nil {
		return err
	}
	routes, problems, err := src.List()
	if err != nil {
		return err
	}

	var imported int
	for _, route := range routes {
		raw, err := src.GPX(route.Slug)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", route.Slug, err))
			continue
		}
		created, err := writable.Create(source.CreateRequest{
			Filename: route.Slug,
			Name:     route.Name,
			Descript: route.Description,
			Tags:     route.Tags,
			Targets:  route.Targets,
			GPX:      raw,
		})
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", route.Slug, err))
			continue
		}
		imported++
		fmt.Printf("imported %s -> %s\n", route.Slug, created.Slug)
	}

	fmt.Printf("\n%d of %d route(s) imported into %s\n", imported, len(routes), dst.Describe())
	return reportProblems(problems)
}

func runState(statePath string) error {
	store, err := state.Open(statePath)
	if err != nil {
		return err
	}

	entries := store.All()
	if len(entries) == 0 {
		fmt.Println("no routes recorded — nothing has been pushed yet")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACCOUNT\tROUTE\tREMOTE ID\tUPDATED")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.AccountID, e.Slug, e.RemoteID, e.UpdatedAt)
	}
	return w.Flush()
}

func runServe(src source.Source, cfg *config.Config, statePath, addr, webDir string) error {
	if _, problems, err := src.List(); err != nil {
		return err
	} else if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "problem:", p)
		}
	}

	store, err := state.Open(statePath)
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv := &api.Server{Source: src, Config: cfg, Store: store, Log: log}

	if info, err := os.Stat(webDir); err == nil && info.IsDir() {
		srv.WebFS = os.DirFS(webDir)
		log.Info("serving web UI", "dir", webDir)
	} else {
		log.Warn("no built frontend found, serving API only", "dir", webDir,
			"hint", "run `just build-web`")
	}

	_, writable := source.AsWritable(src)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Info("listening", "addr", addr, "source", src.Describe(),
		"uploads", writable, "state", statePath)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func printPlan(plan model.Plan) {
	changes := plan.Changes()
	if len(changes) == 0 {
		fmt.Printf("everything up to date (%d route/account pair(s))\n", len(plan.Items))
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "OP\tACCOUNT\tROUTE\tREASON")
	for _, item := range changes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.Op, item.AccountID, item.Slug, item.Reason)
	}
	w.Flush()

	fmt.Printf("\n%d change(s), %d already in sync\n", len(changes), len(plan.Items)-len(changes))
}

func reportProblems(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	fmt.Fprintln(os.Stderr)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "problem:", p)
	}
	return fmt.Errorf("%d problem(s) in the library", len(problems))
}
