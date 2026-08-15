// Package sync is the diff engine: routes + state -> plan, and plan -> execution.
package sync

import (
	"fmt"
	"sort"

	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/state"
	"github.com/wncservices/domestique/apps/api/internal/targets"
)

// BuildPlan compares the routes on offer against recorded remote state, for
// each linked account.
//
// The accounts are passed in rather than read from config: they are linked by
// riders through the UI and live in the database, so the caller fetches them.
//
// It returns an error rather than treating unreadable state as empty: an empty
// plan reads as "nothing to do", but empty *state* means "push everything
// again", and the two must never be confused.
func BuildPlan(routes []model.Route, linked []model.Account, store state.Store) (model.Plan, error) {
	var plan model.Plan

	for _, account := range linked {
		recorded, err := store.ForAccount(account.ID)
		if err != nil {
			return model.Plan{}, fmt.Errorf("read state for %s: %w", account.ID, err)
		}

		desired := map[string]model.Route{}
		for _, route := range routes {
			for _, target := range config.TargetsFor(route, linked) {
				if target == account.ID {
					desired[route.Slug] = route
				}
			}
		}

		for _, slug := range sortedRouteKeys(desired) {
			route := desired[slug]
			known, seen := recorded[slug]
			switch {
			case !seen:
				plan.Items = append(plan.Items, model.PlanItem{
					Op: model.OpCreate, AccountID: account.ID, Slug: slug,
					Route: routePtr(route), Reason: "never pushed",
				})
			case known.ContentHash != route.ContentHash:
				plan.Items = append(plan.Items, model.PlanItem{
					Op: model.OpUpdate, AccountID: account.ID, Slug: slug,
					Route: routePtr(route), RemoteID: known.RemoteID,
					Reason: "route changed since last push",
				})
			default:
				plan.Items = append(plan.Items, model.PlanItem{
					Op: model.OpNoop, AccountID: account.ID, Slug: slug,
					Route: routePtr(route), RemoteID: known.RemoteID,
					Reason: "up to date",
				})
			}
		}

		for _, slug := range sortedEntryKeys(recorded) {
			if _, wanted := desired[slug]; wanted {
				continue
			}
			plan.Items = append(plan.Items, model.PlanItem{
				Op: model.OpDelete, AccountID: account.ID, Slug: slug,
				RemoteID: recorded[slug].RemoteID,
				Reason:   "removed from the library or no longer targeted",
			})
		}
	}

	return plan, nil
}

// Apply executes a plan. It returns per-item failures; one bad route never
// aborts the run, because the other rider's routes should still go out.
func Apply(plan model.Plan, store state.Store, byAccount map[string]targets.Target) []error {
	var failures []error

	for _, item := range plan.Changes() {
		target, ok := byAccount[item.AccountID]
		if !ok {
			failures = append(failures, fmt.Errorf("%s: no configured target adapter", item.AccountID))
			continue
		}

		if err := applyOne(item, store, target); err != nil {
			failures = append(failures, fmt.Errorf("%s %s: %s failed: %w",
				item.AccountID, item.Slug, item.Op, err))
		}
	}

	return failures
}

func applyOne(item model.PlanItem, store state.Store, target targets.Target) error {
	switch item.Op {
	case model.OpCreate:
		remoteID, err := target.Create(*item.Route)
		if err != nil {
			return err
		}
		return store.Record(state.Entry{
			AccountID: item.AccountID, Slug: item.Slug, RemoteID: remoteID,
			ContentHash: item.Route.ContentHash, Name: item.Route.Name,
		})

	case model.OpUpdate:
		remoteID, err := target.Update(item.RemoteID, *item.Route)
		if err != nil {
			return err
		}
		return store.Record(state.Entry{
			AccountID: item.AccountID, Slug: item.Slug, RemoteID: remoteID,
			ContentHash: item.Route.ContentHash, Name: item.Route.Name,
		})

	case model.OpDelete:
		if err := target.Delete(item.RemoteID); err != nil {
			return err
		}
		return store.Forget(item.AccountID, item.Slug)
	}

	return nil
}

func routePtr(r model.Route) *model.Route { return &r }

func sortedRouteKeys(m map[string]model.Route) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedEntryKeys(m map[string]state.Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
