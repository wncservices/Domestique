// Package sync is the diff engine: library + state -> plan, and plan -> execution.
package sync

import (
	"fmt"
	"sort"

	"github.com/wncservices/domestique/apps/api/internal/library"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/state"
	"github.com/wncservices/domestique/apps/api/internal/targets"
)

// BuildPlan compares the library against recorded remote state, per account.
func BuildPlan(lib *library.Library, store state.Store) model.Plan {
	var plan model.Plan

	for _, account := range lib.Config.Accounts {
		recorded := store.ForAccount(account.ID)

		desired := map[string]model.Route{}
		for _, route := range lib.Routes {
			for _, target := range lib.TargetsFor(route) {
				if target == account.ID {
					desired[route.Slug] = route
				}
			}
		}

		for _, slug := range sortedKeys(desired) {
			route := desired[slug]
			known, seen := recorded[slug]
			switch {
			case !seen:
				plan.Items = append(plan.Items, model.PlanItem{
					Op: model.OpCreate, AccountID: account.ID, Slug: slug,
					Route: routePtr(route), Reason: "not on this account yet",
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
				Reason:   "removed from library or no longer targeted",
			})
		}
	}

	return plan
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
			ContentHash: item.Route.ContentHash, Name: item.Route.Name(),
		})

	case model.OpUpdate:
		remoteID, err := target.Update(item.RemoteID, *item.Route)
		if err != nil {
			return err
		}
		return store.Record(state.Entry{
			AccountID: item.AccountID, Slug: item.Slug, RemoteID: remoteID,
			ContentHash: item.Route.ContentHash, Name: item.Route.Name(),
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

func sortedKeys(m map[string]model.Route) []string {
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
