// Command domestique reconciles a git-tracked library of cycling routes into
// each rider's Garmin Connect and Wahoo account.
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
	"github.com/wncservices/domestique/apps/api/internal/library"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/state"
	"github.com/wncservices/domestique/apps/api/internal/sync"
	"github.com/wncservices/domestique/apps/api/internal/targets"
)

const usage = `domestique — fetch-and-carry for cycling routes

usage: domestique <command> [flags]

commands:
  validate   parse the library and report problems
  plan       show what would change on each account
  push       apply the plan (use --dry-run to preview)
  state      list what each account is recorded as holding
  serve      run the HTTP API and the web UI

common flags:
  --library PATH   route library directory (default ./routes)
  --state PATH     state file (default <library>/../.domestique-state.json)

serve flags:
  --addr ADDR      listen address (default :8080)
  --web-dir PATH   built frontend to serve (default apps/web/dist)
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
	libPath := fs.String("library", "routes", "route library directory")
	statePath := fs.String("state", "", "state file (default <library>/../.domestique-state.json)")
	dryRun := fs.Bool("dry-run", false, "print what push would do without doing it")
	addr := fs.String("addr", ":8080", "listen address for serve")
	webDir := fs.String("web-dir", filepath.Join("apps", "web", "dist"), "built frontend to serve")

	switch cmd {
	case "validate", "plan", "push", "state", "serve":
		if err := fs.Parse(rest); err != nil {
			return err
		}
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: domestique help)", cmd)
	}

	if *statePath == "" {
		*statePath = filepath.Join(filepath.Dir(*libPath), ".domestique-state.json")
	}

	switch cmd {
	case "validate":
		return runValidate(*libPath)
	case "plan":
		return runPlan(*libPath, *statePath)
	case "push":
		return runPush(*libPath, *statePath, *dryRun)
	case "state":
		return runState(*statePath)
	case "serve":
		return runServe(*libPath, *statePath, *addr, *webDir)
	}
	return nil
}

func runServe(libPath, statePath, addr, webDir string) error {
	// Fail fast on a broken library rather than serving 500s.
	if _, problems, err := library.Load(libPath); err != nil {
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
	srv := &api.Server{LibraryPath: libPath, Store: store, Log: log}

	if info, err := os.Stat(webDir); err == nil && info.IsDir() {
		srv.WebFS = os.DirFS(webDir)
		log.Info("serving web UI", "dir", webDir)
	} else {
		log.Warn("no built frontend found, serving API only", "dir", webDir,
			"hint", "run `just build-web` (or `npm -w @domestique/web run build`)")
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Info("listening", "addr", addr, "library", libPath, "state", statePath)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runValidate(libPath string) error {
	lib, problems, err := library.Load(libPath)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROUTE\tNAME\tDISTANCE\tASCENT\tPOINTS\tTARGETS")
	for _, r := range lib.Routes {
		fmt.Fprintf(w, "%s\t%s\t%.1f km\t%.0f m\t%d\t%v\n",
			r.Slug, r.Name(), r.Stats.DistanceM/1000, r.Stats.AscentM,
			r.Stats.PointCount, lib.TargetsFor(r))
	}
	w.Flush()

	fmt.Printf("\n%d route(s), %d account(s)\n", len(lib.Routes), len(lib.Config.Accounts))
	return reportProblems(problems)
}

func runPlan(libPath, statePath string) error {
	lib, problems, err := library.Load(libPath)
	if err != nil {
		return err
	}
	store, err := state.Open(statePath)
	if err != nil {
		return err
	}

	printPlan(sync.BuildPlan(lib, store))
	return reportProblems(problems)
}

func runPush(libPath, statePath string, dryRun bool) error {
	lib, problems, err := library.Load(libPath)
	if err != nil {
		return err
	}
	store, err := state.Open(statePath)
	if err != nil {
		return err
	}

	plan := sync.BuildPlan(lib, store)
	printPlan(plan)

	if dryRun {
		fmt.Println("\ndry run — nothing pushed")
		return reportProblems(problems)
	}
	if len(plan.Changes()) == 0 {
		return reportProblems(problems)
	}

	byAccount := map[string]targets.Target{}
	for _, account := range lib.Config.Accounts {
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
