package api

import (
	"context"
	"time"
)

// autoImportInterval is how often the unattended poller checks each
// connected rider's Wahoo/Komoot/Garmin for new routes. None of the three
// push a webhook here — this is the only way to notice something new
// without a human clicking Import.
const autoImportInterval = 30 * time.Minute

// RunAutoImportLoop polls every connected Wahoo/Komoot/Garmin account on
// autoImportInterval, importing anything new — skipping likely duplicates,
// the same check the manual Import buttons already use — and then, if
// anything actually came in, pushing it out to devices. That closes the
// loop auto-sync promises end to end: providers -> library -> devices,
// nobody clicking anything.
//
// Runs until ctx is cancelled (server shutdown) — meant to be started once,
// in its own goroutine, from main.go.
func (s *Server) RunAutoImportLoop(ctx context.Context) {
	// Run once immediately rather than waiting a full interval for the
	// first pass — a freshly deployed or restarted server would otherwise
	// sit for up to autoImportInterval before ever checking.
	s.AutoImportTick(ctx)

	ticker := time.NewTicker(autoImportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.AutoImportTick(ctx)
		}
	}
}

// AutoImportTick is one pass — everything RunAutoImportLoop does on a
// single tick. Exported so a test can drive it directly instead of waiting
// on a real ticker.
func (s *Server) AutoImportTick(ctx context.Context) {
	if s.Settings == nil {
		return
	}
	enabled, err := s.Settings.Flag(FlagAutoSync)
	if err != nil {
		s.logger().Warn("auto-import: could not read the auto-sync flag", "err", err)
		return
	}
	// Checked first, before any provider is touched: a disabled deployment
	// must make zero third-party API calls, not merely skip acting on
	// what it found.
	if !enabled {
		return
	}

	imported := s.autoImportGarmin(ctx) + s.autoImportWahoo(ctx) + s.autoImportKomoot(ctx)
	if imported == 0 {
		return
	}
	s.logger().Info("auto-import finished", "imported", imported)

	// autoPushOnly: this is the unattended path, so it honors each
	// account's own auto-push preference — see runPush's own doc comment.
	if _, err := s.runPush(ctx, nil, true, nil); err != nil {
		s.logger().Error("auto-import: push after import failed", "err", err)
	}
}

// autoImportGarmin pulls in every new course on every rider's own connected
// Garmin account. "New" means not already tracked as synced (the exact
// check garminTrackedRemoteIDs already does) and not a likely duplicate of
// something already in the library (possibleDuplicateOf, the same
// distance-and-start-point heuristic handleGarminCourseList shows in the UI)
// — an unattended poller leaves anything ambiguous for a rider to look at
// through the ordinary Import screen instead of silently duplicating it.
func (s *Server) autoImportGarmin(ctx context.Context) int {
	if s.Garmin == nil || s.Links == nil {
		return 0
	}
	riders, err := s.Links.ListRiders(garminProvider)
	if err != nil {
		s.logger().Warn("auto-import: listing garmin riders failed", "err", err)
		return 0
	}

	imported := 0
	for _, rider := range riders {
		session, ok := s.garminSessionForRider(rider)
		if !ok {
			continue
		}
		consumer, _ := s.garminConsumer()
		courses, err := s.Garmin.ListCourses(ctx, consumer, session)
		if err != nil {
			s.logger().Warn("auto-import: garmin course list failed", "rider", rider, "err", err)
			continue
		}
		routes, _, err := s.Source.List(ctx)
		if err != nil {
			s.logger().Warn("auto-import: reading the library failed", "err", err)
			continue
		}
		tracked := s.garminTrackedRemoteIDs(ctx, rider)

		var wanted []string
		for _, c := range courses {
			if tracked[c.ID] || possibleDuplicateOf(c, routes) != "" {
				continue
			}
			wanted = append(wanted, c.ID)
		}
		if len(wanted) == 0 {
			continue
		}

		result, err := s.importGarminCourses(ctx, rider, rider, session, wanted)
		if err != nil {
			s.logger().Warn("auto-import: garmin import failed", "rider", rider, "err", err)
			continue
		}
		imported += len(result.Imported)
	}
	return imported
}

// autoImportWahoo is autoImportGarmin's twin for Wahoo — same "not tracked,
// not a likely duplicate" filter, using possibleWahooDuplicateOf.
func (s *Server) autoImportWahoo(ctx context.Context) int {
	if s.Wahoo == nil || s.Links == nil {
		return 0
	}
	riders, err := s.Links.ListRiders(wahooProvider)
	if err != nil {
		s.logger().Warn("auto-import: listing wahoo riders failed", "err", err)
		return 0
	}

	imported := 0
	for _, rider := range riders {
		token, err := s.wahooAccessToken(ctx, rider)
		if err != nil {
			continue
		}
		routes, err := s.Wahoo.ListRoutes(ctx, token)
		if err != nil {
			s.logger().Warn("auto-import: wahoo route list failed", "rider", rider, "err", err)
			continue
		}
		library, _, err := s.Source.List(ctx)
		if err != nil {
			s.logger().Warn("auto-import: reading the library failed", "err", err)
			continue
		}
		tracked := s.wahooTrackedRemoteIDs(ctx, rider)

		var wanted []string
		for _, rt := range routes {
			if tracked[rt.ID] || possibleWahooDuplicateOf(rt, library) != "" {
				continue
			}
			wanted = append(wanted, rt.ID)
		}
		if len(wanted) == 0 {
			continue
		}

		result, err := s.importWahooRoutes(ctx, rider, rider, token, wanted)
		if err != nil {
			s.logger().Warn("auto-import: wahoo import failed", "rider", rider, "err", err)
			continue
		}
		imported += len(result.Imported)
	}
	return imported
}

// autoImportKomoot pulls in every planned tour on every rider's own
// connected Komoot account. No separate duplicate filter here, unlike
// Garmin and Wahoo: Komoot dedup is already an exact tag match
// (komootTagIndex, inside importKomootTours), not a fuzzy heuristic, so
// there is nothing "likely" left to second-guess — every listed tour is
// simply offered, and anything already imported comes back Skipped rather
// than duplicated.
func (s *Server) autoImportKomoot(ctx context.Context) int {
	if s.Links == nil {
		return 0
	}
	riders, err := s.Links.ListRiders(komootProvider)
	if err != nil {
		s.logger().Warn("auto-import: listing komoot riders failed", "err", err)
		return 0
	}

	imported := 0
	for _, rider := range riders {
		client := s.komootClientForRider(rider)
		if client == nil {
			continue
		}
		tours, err := client.Tours(ctx, s.Config.Komoot.IncludeRecorded)
		if err != nil {
			s.logger().Warn("auto-import: komoot tour list failed", "rider", rider, "err", err)
			continue
		}
		if len(tours) == 0 {
			continue
		}
		ids := make([]string, 0, len(tours))
		for _, t := range tours {
			ids = append(ids, t.ID)
		}

		result, err := s.importKomootTours(ctx, rider, client, ids)
		if err != nil {
			s.logger().Warn("auto-import: komoot import failed", "rider", rider, "err", err)
			continue
		}
		imported += len(result.Imported)
	}
	return imported
}
