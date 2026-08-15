package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/dbx"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/providerlink"
	"github.com/wncservices/domestique/apps/api/internal/source"
)

// normalizeRider matches accounts.ID's own normalization
// (strings.ToLower(strings.TrimSpace(rider))) — the same rider string has to
// normalize identically everywhere or a rename could target a row that a
// case- or whitespace-difference means was never actually renamed.
func normalizeRider(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// renameSummary is how many rows in each table carried the old rider,
// counted whether or not this run actually wrote anything — a --dry-run
// needs to report the same numbers a real run would.
type renameSummary struct {
	routes, accounts, syncState, providerLinks int
	// replacedAccounts, replacedSyncState and replacedProviderLinks count
	// what --replace deleted on the new rider's side to clear a conflict —
	// zero whenever --replace was not asked for, since nothing is ever
	// deleted without it.
	replacedAccounts, replacedSyncState, replacedProviderLinks int
}

// runRenameRider is the one-off migration docs/rider-migration.md walks an
// operator through: moving every row keyed to an old rider identity onto a
// new one, in a single transaction, once.
//
// This is deliberately a CLI command working directly against src.Conn()
// rather than new methods added to accounts/providerlink/state. Those
// packages never otherwise need a multi-table write or real transactional
// atomicity — every existing method touches one table — and growing their
// public API for an operation that runs exactly once per rider, ever, is the
// wrong trade. The CLI already has direct database access and is where
// keygen already lives as a comparable special case.
func runRenameRider(src *source.DB, args []string, dryRun, replace bool) error {
	if len(args) != 2 {
		return errors.New("rename-rider needs exactly two arguments: <old-rider> <new-rider>\n" +
			"       see docs/rider-migration.md before running this for real")
	}
	old, next := normalizeRider(args[0]), normalizeRider(args[1])
	if old == "" || next == "" {
		return errors.New("rename-rider: neither rider may be empty")
	}
	if old == next {
		return errors.New("rename-rider: old and new are the same rider — nothing to do")
	}
	if !accounts.RiderPattern.MatchString(next) {
		return fmt.Errorf("rename-rider: %q has characters that cannot appear in an account id", next)
	}

	d, err := dbx.For(src.DSN())
	if err != nil {
		return err
	}
	// provider_links is only ever created by the API server (providerlink.UseDB),
	// which this CLI path never runs. A box is not needed — this touches only
	// the rider column, never a sealed secret — so nil is fine; UseDB's table
	// creation is the only reason to call it here.
	if _, err := providerlink.UseDB(src.Conn(), src.DSN(), nil); err != nil {
		return err
	}

	sum, err := renameRiderTx(src.Conn(), d, old, next, dryRun, replace)
	if err != nil {
		return err
	}

	fmt.Printf("routes:              %d\n", sum.routes)
	fmt.Printf("accounts:            %d\n", sum.accounts)
	fmt.Printf("sync state rows:     %d\n", sum.syncState)
	fmt.Printf("provider sign-ins:   %d\n", sum.providerLinks)
	if replace && (sum.replacedAccounts > 0 || sum.replacedSyncState > 0 || sum.replacedProviderLinks > 0) {
		fmt.Printf("\nreplaced on conflict (deleted, %s's row took over):\n", next)
		fmt.Printf("  accounts:          %d\n", sum.replacedAccounts)
		fmt.Printf("  sync state rows:   %d\n", sum.replacedSyncState)
		fmt.Printf("  provider sign-ins: %d\n", sum.replacedProviderLinks)
	}
	if dryRun {
		fmt.Println("\ndry run — nothing written")
	} else {
		fmt.Printf("\nrenamed %q to %q\n", old, next)
	}
	return nil
}

// renameRiderTx does the actual work, inside one transaction so a failure
// partway through leaves nothing half-renamed. Rollback on the deferred path
// is always safe: it is a no-op once Commit has succeeded.
//
// Every count is read before anything is written, in both the real and the
// dry-run case, so a --dry-run reports the same numbers a real run would —
// counting sync_state rows after already renaming their account_id would
// find nothing, since by then they carry the new id.
func renameRiderTx(db *sql.DB, d dbx.Dialect, old, next string, dryRun, replace bool) (renameSummary, error) {
	var sum renameSummary

	tx, err := db.Begin()
	if err != nil {
		return sum, err
	}
	defer func() { _ = tx.Rollback() }()

	// Every statement below is a constant literal with every value bound —
	// the same shape as providerlink.go and settings.go, which gosec's taint
	// analysis flags anyway because it cannot see through Rebind. #nosec G701
	// on each one, rather than one per line below repeating the same note.

	// accounts.id is a derived composite key ("<provider>:<rider>"), and
	// sync_state.account_id is that same string as half of its own primary
	// key — so an account and its sync state have to be renamed together, or
	// the sync state is silently orphaned and every route looks like it needs
	// pushing again from scratch.
	// #nosec G701
	acctRows, err := tx.Query(d.Rebind(`SELECT id, provider FROM accounts WHERE rider = ?`), old)
	if err != nil {
		return sum, fmt.Errorf("reading %s's accounts: %w", old, err)
	}
	type acctPair struct{ oldID, provider string }
	var pairs []acctPair
	for acctRows.Next() {
		var p acctPair
		if err := acctRows.Scan(&p.oldID, &p.provider); err != nil {
			_ = acctRows.Close()
			return sum, err
		}
		pairs = append(pairs, p)
	}
	if err := acctRows.Err(); err != nil {
		_ = acctRows.Close()
		return sum, err
	}
	_ = acctRows.Close()

	for _, p := range pairs {
		newID := accounts.ID(model.Provider(p.provider), next)

		var conflict int
		// #nosec G701
		if err := tx.QueryRow(d.Rebind(`SELECT COUNT(1) FROM accounts WHERE id = ?`), newID).
			Scan(&conflict); err != nil {
			return sum, err
		}
		if conflict > 0 {
			if !replace {
				return sum, fmt.Errorf(
					"rename-rider: %s already has a %s account (%s) — resolve that conflict first, then retry",
					next, p.provider, newID)
			}
			// --replace's contract: the old rider's row wins, so the new
			// rider's conflicting account (and whatever sync state still
			// points at it) is deleted first, clearing the id for the
			// UPDATE below to claim.
			var staleStateRows int
			// #nosec G701
			if err := tx.QueryRow(d.Rebind(`SELECT COUNT(1) FROM sync_state WHERE account_id = ?`), newID).
				Scan(&staleStateRows); err != nil {
				return sum, err
			}
			sum.replacedAccounts++
			sum.replacedSyncState += staleStateRows
			if !dryRun {
				// #nosec G701
				if _, err := tx.Exec(d.Rebind(`DELETE FROM sync_state WHERE account_id = ?`), newID); err != nil {
					return sum, fmt.Errorf("clearing %s's stale sync state: %w", newID, err)
				}
				// #nosec G701
				if _, err := tx.Exec(d.Rebind(`DELETE FROM accounts WHERE id = ?`), newID); err != nil {
					return sum, fmt.Errorf("clearing %s's stale account: %w", newID, err)
				}
			}
		}

		var stateRows int
		// #nosec G701
		if err := tx.QueryRow(d.Rebind(`SELECT COUNT(1) FROM sync_state WHERE account_id = ?`), p.oldID).
			Scan(&stateRows); err != nil {
			return sum, err
		}
		sum.accounts++
		sum.syncState += stateRows

		if dryRun {
			continue
		}
		// #nosec G701
		if _, err := tx.Exec(d.Rebind(`UPDATE accounts SET id = ?, rider = ? WHERE id = ?`),
			newID, next, p.oldID); err != nil {
			return sum, fmt.Errorf("renaming account %s: %w", p.oldID, err)
		}
		if stateRows > 0 {
			// #nosec G701
			if _, err := tx.Exec(d.Rebind(`UPDATE sync_state SET account_id = ? WHERE account_id = ?`),
				newID, p.oldID); err != nil {
				return sum, fmt.Errorf("renaming sync state for %s: %w", p.oldID, err)
			}
		}
	}

	// provider_links: composite key (provider, rider), no derived id to carry
	// anywhere else.
	// #nosec G701
	linkRows, err := tx.Query(d.Rebind(`SELECT provider FROM provider_links WHERE rider = ?`), old)
	if err != nil {
		return sum, fmt.Errorf("reading %s's provider sign-ins: %w", old, err)
	}
	var providers []string
	for linkRows.Next() {
		var provider string
		if err := linkRows.Scan(&provider); err != nil {
			_ = linkRows.Close()
			return sum, err
		}
		providers = append(providers, provider)
	}
	if err := linkRows.Err(); err != nil {
		_ = linkRows.Close()
		return sum, err
	}
	_ = linkRows.Close()

	for _, provider := range providers {
		var conflict int
		// #nosec G701
		if err := tx.QueryRow(d.Rebind(`SELECT COUNT(1) FROM provider_links WHERE provider = ? AND rider = ?`),
			provider, next).Scan(&conflict); err != nil {
			return sum, err
		}
		if conflict > 0 {
			if !replace {
				return sum, fmt.Errorf(
					"rename-rider: %s already has a %s sign-in — resolve that conflict first, then retry",
					next, provider)
			}
			sum.replacedProviderLinks++
			if !dryRun {
				// #nosec G701
				if _, err := tx.Exec(d.Rebind(`DELETE FROM provider_links WHERE provider = ? AND rider = ?`),
					provider, next); err != nil {
					return sum, fmt.Errorf("clearing %s's stale %s sign-in: %w", next, provider, err)
				}
			}
		}
	}
	sum.providerLinks = len(providers)
	if !dryRun && len(providers) > 0 {
		// #nosec G701
		if _, err := tx.Exec(d.Rebind(`UPDATE provider_links SET rider = ? WHERE rider = ?`),
			next, old); err != nil {
			return sum, fmt.Errorf("renaming provider sign-ins: %w", err)
		}
	}

	// routes.uploaded_by carries no key, so no conflict is possible.
	// #nosec G701
	if err := tx.QueryRow(d.Rebind(`SELECT COUNT(1) FROM routes WHERE uploaded_by = ?`), old).
		Scan(&sum.routes); err != nil {
		return sum, err
	}
	if !dryRun && sum.routes > 0 {
		// #nosec G701
		if _, err := tx.Exec(d.Rebind(`UPDATE routes SET uploaded_by = ? WHERE uploaded_by = ?`),
			next, old); err != nil {
			return sum, fmt.Errorf("renaming uploaded routes: %w", err)
		}
	}

	if dryRun {
		return sum, nil // rolled back by the deferred Rollback; nothing was ever written
	}
	if err := tx.Commit(); err != nil {
		return sum, err
	}
	return sum, nil
}
