package rallly

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/lporcheron/quorum/internal/config"
	"github.com/lporcheron/quorum/internal/store"
)

// RunCLI implements the `quorum import-rallly` subcommand: it migrates a
// self-hosted Rallly instance into this Quorum instance from a
// plain-format pg_dump.
//
//	pg_dump --format=plain rallly > rallly.sql
//	quorum import-rallly -dump rallly.sql -dry-run
//	quorum import-rallly -dump rallly.sql
//
// The target database defaults to the one the server itself would open
// (QUORUM_DB_PATH, or DATABASE_URL for PostgreSQL), so a containerized
// instance needs no flag beyond -dump. -db and -database-url override it.
func RunCLI(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("import-rallly", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprint(stdout, "Usage: quorum import-rallly -dump <rallly.sql> [flags]\n\n"+
			"Imports a plain-format Rallly pg_dump into this instance's database.\n"+
			"Without -db or -database-url, the server's own configuration is used.\n\n")
		fs.PrintDefaults()
	}
	dumpPath := fs.String("dump", "", "path to the Rallly pg_dump (plain format, required)")
	dbPath := fs.String("db", "", "target SQLite database (default: the server's QUORUM_DB_PATH)")
	databaseURL := fs.String("database-url", "", "target PostgreSQL URL (default: the server's DATABASE_URL)")
	force := fs.Bool("force", false, "import even if the target already contains polls")
	dryRun := fs.Bool("dry-run", false, "report what would be imported, then roll back")
	linksOut := fs.String("links-out", "", "write guest admin links to this file (mode 0600) instead of stdout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil // -h already printed the usage; that is a success
		}
		return err
	}
	if *dumpPath == "" {
		fs.Usage()
		return errors.New("-dump is required")
	}

	// Fall back to the server's own target, so the subcommand cannot
	// silently import into a different database than the one it serves.
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	if *dbPath == "" {
		*dbPath = cfg.DBPath
	}
	if *databaseURL == "" {
		*databaseURL = cfg.DatabaseURL
	}

	dump, err := os.Open(*dumpPath)
	if err != nil {
		return err
	}
	defer func() { _ = dump.Close() }()

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

	target := *dbPath
	if dialect == store.DialectPostgres {
		target = "postgresql"
	}
	fmt.Fprintf(stdout, "Target: %s\n", target)

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

	summary, err := Import(ctx, st, dump, Options{Now: time.Now(), DryRun: *dryRun})
	if err != nil {
		discardLinksFile(links, *linksOut)
		return err
	}

	verb := "Imported"
	if *dryRun {
		verb = "Would import"
	}
	fmt.Fprintf(stdout, "%s: %d users, %d shared spaces, %d polls (%d finalized, %d without an account), %d options, %d participants, %d votes, %d comments\n",
		verb, summary.Users, summary.Spaces, summary.Polls, summary.Finalized, summary.GuestPolls,
		summary.Options, summary.Participants, summary.Votes, summary.Comments)
	for _, note := range summary.Skipped {
		fmt.Fprintf(stdout, "skipped: %s\n", note)
	}

	switch {
	case *dryRun:
		// Pending schema migrations did run: only the import itself was
		// rolled back. Say so rather than promise an untouched file.
		fmt.Fprint(stdout, "\nDry run: the transaction was rolled back, so no data was written. Guest admin links are minted on the real run.\n")
	case len(summary.GuestAdminLinks) > 0 && links != nil:
		if err := writeLinks(links, summary.GuestAdminLinks); err != nil {
			// The import is committed; the links are the only thing at
			// risk, so print them rather than lose them.
			fmt.Fprintf(os.Stderr, "%v — printing the links instead:\n", err)
			printLinks(stdout, summary.GuestAdminLinks)
			return nil
		}
		fmt.Fprintf(stdout, "\nWrote %d guest admin links to %s (mode 0600). Hand them to their organizers — they cannot be shown again.\n",
			len(summary.GuestAdminLinks), *linksOut)
	case len(summary.GuestAdminLinks) > 0:
		fmt.Fprint(stdout, "\nGuest polls (no account): hand these admin links to their organizers — shown only once. Use -links-out to write them to a file instead:\n")
		printLinks(stdout, summary.GuestAdminLinks)
	default:
		// No guest polls: an empty file would only confuse.
		discardLinksFile(links, *linksOut)
	}

	fmt.Fprint(stdout, "\nRegistered users sign in with their usual email or OAuth account; the verified-email merge attaches their history. Participant edit links were not migrated: voters vote anew or ask the organizer.\n")
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

func printLinks(w io.Writer, links map[string]string) {
	for _, pollID := range sortedKeys(links) {
		fmt.Fprintf(w, "  /polls/%s/admin/%s\n", pollID, links[pollID])
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
