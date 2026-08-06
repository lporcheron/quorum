// Command rallly-import migrates a Rallly instance's data into Quorum
// from a plain-format pg_dump:
//
//	pg_dump --format=plain rallly > rallly.sql
//	rallly-import -dump rallly.sql -db quorum.db -dry-run
//	rallly-import -dump rallly.sql -db quorum.db
//	rallly-import -dump rallly.sql -database-url postgres://…
//
// Registered users are recreated by email (Rallly has no passwords;
// they sign in on Quorum and the verified-email merge reunites them
// with their polls). Anonymous guests are not imported as accounts:
// their polls come out as claimable guest polls, whose fresh admin
// links are printed once at the end.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/lporcheron/quorum/internal/rallly"
	"github.com/lporcheron/quorum/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rallly-import:", err)
		os.Exit(1)
	}
}

func run() error {
	dumpPath := flag.String("dump", "", "path to the Rallly pg_dump (plain format)")
	dbPath := flag.String("db", "quorum.db", "target SQLite database")
	databaseURL := flag.String("database-url", "", "target PostgreSQL URL (overrides -db)")
	force := flag.Bool("force", false, "import even if the target already contains polls")
	dryRun := flag.Bool("dry-run", false, "report what would be imported, then roll back")
	linksOut := flag.String("links-out", "", "write guest admin links to this file (mode 0600) instead of stdout")
	flag.Parse()
	if *dumpPath == "" {
		return fmt.Errorf("-dump is required")
	}

	dump, err := os.Open(*dumpPath)
	if err != nil {
		return err
	}
	defer func() { _ = dump.Close() }()

	ctx := context.Background()
	dialect := store.DialectSQLite
	var db *sql.DB
	if *databaseURL != "" {
		dialect = store.DialectPostgres
		db, err = store.OpenPostgres(ctx, *databaseURL)
	} else {
		db, err = store.Open(ctx, *dbPath)
	}
	if err != nil {
		return err
	}
	defer db.Close()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := store.Migrate(ctx, db, dialect, log); err != nil {
		return err
	}
	st := store.New(db, dialect)

	existing, err := st.CountPolls(ctx)
	if err != nil {
		return err
	}
	if existing > 0 && !*force {
		return fmt.Errorf("target already contains %d polls; re-run with -force to import anyway", existing)
	}

	// Claim the links file before importing, not after: the admin tokens
	// exist only in memory, so discovering an unwritable path once the
	// transaction has committed would lose them for good.
	var links *os.File
	if *linksOut != "" && !*dryRun {
		links, err = os.OpenFile(*linksOut, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("cannot write the guest admin links: %w", err)
		}
	}

	summary, err := rallly.Import(ctx, st, dump, rallly.Options{Now: time.Now(), DryRun: *dryRun})
	if err != nil {
		discardLinksFile(links, *linksOut)
		return err
	}

	verb := "Imported"
	if *dryRun {
		verb = "Would import"
	}
	fmt.Printf("%s: %d users, %d shared spaces, %d polls (%d finalized, %d without an account), %d options, %d participants, %d votes, %d comments\n",
		verb, summary.Users, summary.Spaces, summary.Polls, summary.Finalized, summary.GuestPolls,
		summary.Options, summary.Participants, summary.Votes, summary.Comments)
	for _, note := range summary.Skipped {
		fmt.Println("skipped:", note)
	}

	switch {
	case *dryRun:
		// Pending schema migrations did run: only the import itself was
		// rolled back. Say so rather than promise an untouched file.
		fmt.Println("\nDry run: the transaction was rolled back, so no data was written. Guest admin links are minted on the real run.")
	case len(summary.GuestAdminLinks) > 0 && links != nil:
		if err := writeLinks(links, summary.GuestAdminLinks); err != nil {
			// The import is committed; the links are the only thing at
			// risk, so print them rather than lose them.
			fmt.Fprintln(os.Stderr, "rallly-import:", err, "— printing the links instead:")
			printLinks(summary.GuestAdminLinks)
			return nil
		}
		fmt.Printf("\nWrote %d guest admin links to %s (mode 0600). Hand them to their organizers — they cannot be shown again.\n",
			len(summary.GuestAdminLinks), *linksOut)
	case len(summary.GuestAdminLinks) > 0:
		fmt.Println("\nGuest polls (no account): hand these admin links to their organizers — shown only once. Use -links-out to write them to a file instead:")
		printLinks(summary.GuestAdminLinks)
	default:
		// No guest polls: an empty file would only confuse.
		discardLinksFile(links, *linksOut)
	}

	fmt.Println("\nRegistered users sign in with their usual email or OAuth account; the verified-email merge attaches their history. Participant edit links were not migrated: voters vote anew or ask the organizer.")
	return nil
}

// writeLinks saves the one-shot admin links into the file claimed
// before the import. They are capability URLs, so the file is
// owner-readable only: a terminal scrollback is a poor vault.
func writeLinks(f *os.File, links map[string]string) error {
	for _, pollID := range sortedKeys(links) {
		if _, err := fmt.Fprintf(f, "/polls/%s/admin/%s\n", pollID, links[pollID]); err != nil {
			_ = f.Close()
			return fmt.Errorf("write admin links: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write admin links: %w", err)
	}
	return nil
}

func printLinks(links map[string]string) {
	for _, pollID := range sortedKeys(links) {
		fmt.Printf("  /polls/%s/admin/%s\n", pollID, links[pollID])
	}
}

// discardLinksFile removes the file claimed up front when there is
// nothing to put in it.
func discardLinksFile(f *os.File, path string) {
	if f == nil {
		return
	}
	_ = f.Close()
	_ = os.Remove(path)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
