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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/api"
	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/fitcourse"
	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/komoot"
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
  komoot     list or import routes from a Komoot account
  fit        export a route as a Garmin FIT course
  serve      run the HTTP API and the web UI
  version    print the version

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

fit:
  domestique fit <slug> [--out FILE] [--cues]

  Writes a FIT course, which can be copied straight onto a device over USB.
  --cues adds turn cues inferred from the track's shape; they are a heuristic,
  so check them before trusting them on a ride.

komoot:
  domestique komoot list             show the account's planned routes
  domestique komoot import [ids...]  import them (all planned routes if no ids)

  Credentials come from KOMOOT_EMAIL and KOMOOT_PASSWORD in the environment.
`

// version is set at build time: -ldflags="-X main.version=v1.2.3".
var version = "dev"

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
	out := fs.String("out", "", "file to write (default <slug>.fit)")
	cues := fs.Bool("cues", false, "add turn cues inferred from the track's shape")

	var positional []string

	switch cmd {
	case "validate", "plan", "push", "state", "serve", "import", "komoot", "fit":
		// Go's flag package stops at the first positional argument, so
		// `fit <slug> --cues` would silently ignore --cues. Parse in a loop,
		// peeling off positionals, so flags and arguments can interleave in
		// whatever order reads naturally.
		var err error
		if positional, err = parseInterleaved(fs, rest); err != nil {
			return err
		}
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	case "-v", "--version", "version":
		fmt.Println("domestique", version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: domestique help)", cmd)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	applyOverrides(cfg, *sourceKind, *libPath, *dsn)
	if err := cfg.Validate(); err != nil {
		return err
	}

	src, err := openSource(cfg)
	if err != nil {
		return err
	}
	if closer, ok := src.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}

	store, err := openState(src, *statePath)
	if err != nil {
		return err
	}

	linkedAccounts, err := openAccounts(src)
	if err != nil {
		return err
	}
	if closer, ok := store.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}

	switch cmd {
	case "validate":
		return runValidate(src, linkedAccounts)
	case "plan":
		return runPlan(src, linkedAccounts, store)
	case "push":
		return runPush(src, linkedAccounts, store, *dryRun)
	case "import":
		return runImport(src, *from)
	case "state":
		return runState(store)
	case "serve":
		return runServe(src, cfg, store, *addr, *webDir)
	case "komoot":
		return runKomoot(src, cfg, positional)
	case "fit":
		return runFIT(src, positional, *out, *cues)
	}
	return nil
}

// runFIT writes a route out as a FIT course.
//
// This is how the conversion gets proven: copy the file onto a real head unit
// and see whether it navigates. Nothing in a test suite can establish that.
func runFIT(src source.Source, args []string, out string, cues bool) error {
	if len(args) == 0 {
		return errors.New("fit needs a route slug (see: domestique validate)")
	}
	slug := args[0]

	raw, err := src.GPX(slug)
	if err != nil {
		return err
	}
	points, err := gpx.ParsePoints(raw)
	if err != nil {
		return err
	}

	name := slug
	if routes, _, listErr := src.List(); listErr == nil {
		for _, route := range routes {
			if route.Slug == slug {
				name = route.Name
				break
			}
		}
	}

	fitBytes, err := fitcourse.Encode(points, fitcourse.Options{Name: name, TurnCues: cues})
	if err != nil {
		return err
	}

	if out == "" {
		// Derive the filename from the slug, but never let a slug decide where
		// on disk the file lands: flatten separators and take the base, so the
		// result is always a plain name in the working directory.
		out = filepath.Base(strings.NewReplacer("/", "-", `\`, "-").Replace(slug)) + ".fit"
	}

	// #nosec G703 -- --out is an operator-supplied path, the same as any
	// shell redirect; a slug-derived name is flattened above.
	if err := os.WriteFile(out, fitBytes, 0o600); err != nil {
		return err
	}

	turns := 0
	if cues {
		turns = len(fitcourse.Turns(points))
	}
	fmt.Printf("wrote %s (%d bytes, %d points", out, len(fitBytes), len(points))
	if cues {
		fmt.Printf(", %d turn cue(s)", turns)
	}
	fmt.Println(")")
	return nil
}

// komootClient logs in using the environment. Credentials never come from the
// config file — that file is meant to be readable, and this is a password.
func komootClient() (*komoot.Client, error) {
	email, password := os.Getenv("KOMOOT_EMAIL"), os.Getenv("KOMOOT_PASSWORD")
	if email == "" || password == "" {
		return nil, errors.New("set KOMOOT_EMAIL and KOMOOT_PASSWORD to use Komoot")
	}

	client := komoot.New()
	if err := client.Login(email, password); err != nil {
		return nil, err
	}
	return client, nil
}

func runKomoot(dst source.Source, cfg *config.Config, args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}

	client, err := komootClient()
	if err != nil {
		return err
	}

	tours, err := client.Tours(cfg.Komoot.IncludeRecorded)
	if err != nil {
		return err
	}

	switch sub {
	case "list":
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSPORT\tDISTANCE\tASCENT")
		for _, t := range tours {
			fmt.Fprintf(w, "%s\t%s\t%s\t%.1f km\t%.0f m\n",
				t.ID, t.Name, t.Sport, t.DistanceM/1000, t.AscentM)
		}
		w.Flush()
		fmt.Printf("\n%d tour(s) in %s's Komoot account\n", len(tours), client.DisplayName())
		return nil

	case "import":
		writable, ok := source.AsWritable(dst)
		if !ok {
			return fmt.Errorf("%s is read-only; Komoot import needs a database source",
				dst.Describe())
		}

		wanted := map[string]bool{}
		for _, id := range args {
			wanted[id] = true
		}

		var imported int
		var problems []string
		for _, tour := range tours {
			if len(wanted) > 0 && !wanted[tour.ID] {
				continue
			}

			raw, err := client.GPX(tour.ID)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s (%s): %v", tour.Name, tour.ID, err))
				continue
			}
			if _, err := writable.Create(source.CreateRequest{
				Filename: tour.Name + ".gpx",
				Name:     tour.Name,
				Descript: fmt.Sprintf("Imported from Komoot (tour %s)", tour.ID),
				Tags:     []string{"komoot", "komoot:" + tour.ID},
				GPX:      raw,
			}); err != nil {
				problems = append(problems, fmt.Sprintf("%s (%s): %v", tour.Name, tour.ID, err))
				continue
			}
			imported++
			fmt.Printf("imported %s (%s)\n", tour.Name, tour.ID)
		}

		fmt.Printf("\n%d tour(s) imported into %s\n", imported, dst.Describe())
		return reportProblems(problems)

	default:
		return fmt.Errorf("unknown komoot subcommand %q (want list or import)", sub)
	}
}

// parseInterleaved parses flags that may appear before, after or between
// positional arguments, and returns the positionals in order.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string

	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}

	return positional, nil
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
	case config.SourceFS:
		return source.NewFS(cfg.Source.Path)
	default:
		// Unreachable via Validate, but never silently pick a source.
		return nil, fmt.Errorf("unknown source kind %q", cfg.Source.Kind)
	}
}

// openAccounts reads the linked head units.
//
// They live in the database beside the routes, put there by riders through the
// UI. A directory-backed library has no database, so there is nothing to link
// against and the CLI reports none — plan and push then have nothing to do,
// which is the honest answer.
func openAccounts(src source.Source) ([]model.Account, error) {
	store, err := accountStoreFor(src)
	if err != nil || store == nil {
		return nil, err
	}
	return store.List()
}

// accountStoreFor builds the accounts store the API links through.
//
// Linking needs somewhere to write, so it needs a database. A directory-backed
// library has none, and the API says so rather than pretending.
func accountStoreFor(src source.Source) (*accounts.Store, error) {
	db, ok := src.(*source.DB)
	if !ok {
		return nil, nil
	}
	return accounts.UseDB(db.Conn(), db.DSN())
}

// openState decides where sync state lives.
//
// With a database source it goes in that same database, which is the whole
// point: a deployment then needs one database and no volume. A directory
// source has no database to borrow, so it falls back to the JSON file.
func openState(src source.Source, path string) (state.Store, error) {
	if db, ok := src.(*source.DB); ok {
		return state.UseDB(db.Conn(), db.DSN())
	}
	return state.Open(path)
}

func runValidate(src source.Source, linked []model.Account) error {
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
			r.Stats.PointCount, config.TargetsFor(r, linked))
		for _, unknown := range config.UnknownTargets(r, linked) {
			problems = append(problems, fmt.Sprintf("%s: unknown target %q", r.Slug, unknown))
		}
	}
	w.Flush()

	fmt.Printf("\n%d route(s), %d linked account(s)\n", len(routes), len(linked))
	return reportProblems(problems)
}

func runPlan(src source.Source, linked []model.Account, store state.Store) error {
	routes, problems, err := src.List()
	if err != nil {
		return err
	}

	plan, err := sync.BuildPlan(routes, linked, store)
	if err != nil {
		return err
	}

	printPlan(plan)
	return reportProblems(problems)
}

func runPush(src source.Source, linked []model.Account, store state.Store, dryRun bool) error {
	routes, problems, err := src.List()
	if err != nil {
		return err
	}

	plan, err := sync.BuildPlan(routes, linked, store)
	if err != nil {
		return err
	}
	printPlan(plan)

	if dryRun {
		fmt.Println("\ndry run — nothing pushed")
		return reportProblems(problems)
	}
	if len(plan.Changes()) == 0 {
		return reportProblems(problems)
	}

	byAccount := map[string]targets.Target{}
	for _, account := range linked {
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

// runState prints what each account is recorded as holding.
//
// It reads through the same store the rest of the app uses, so it shows the
// truth whether that is a database table or a file. It used to open the file
// directly, which quietly reported "nothing pushed yet" once state moved into
// the database.
func runState(store state.Store) error {
	fmt.Println(describeStore(store))

	entries, err := store.All()
	if err != nil {
		return err
	}
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

func runServe(src source.Source, cfg *config.Config, store state.Store, addr, webDir string) error {
	if _, problems, err := src.List(); err != nil {
		return err
	} else if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "problem:", p)
		}
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	authenticator, err := auth.New(cfg.Auth)
	if err != nil {
		return err
	}

	accountStore, err := accountStoreFor(src)
	if err != nil {
		return err
	}

	srv := &api.Server{
		Source:   src,
		Config:   cfg,
		Store:    store,
		Accounts: accountStore,
		Auth:     authenticator,
		Log:      log,
	}

	if cfg.Komoot.Enabled {
		client, err := komootClient()
		if err != nil {
			// Komoot is optional. Losing it must not stop the app serving
			// routes that are already here.
			log.Warn("komoot import disabled", "err", err)
		} else {
			srv.Komoot = client
			log.Info("komoot import enabled", "account", client.DisplayName())
		}
	}

	if !authenticator.Enabled() {
		log.Warn("running without authentication — every visitor is an admin",
			"hint", "set auth.mode: proxy behind Authelia before exposing this")
	}

	// #nosec G703 -- webDir is an operator-supplied flag, not user input.
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
		"uploads", writable, "auth", authenticator.Mode(), "state", describeStore(store))
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// describeStore names where state lives, for the startup log.
func describeStore(store state.Store) string {
	if d, ok := store.(interface{ Describe() string }); ok {
		return d.Describe()
	}
	return "file"
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
