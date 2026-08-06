// Package rallly imports a Rallly PostgreSQL dump (plain pg_dump with
// COPY blocks) into a Quorum database. It reads Rallly's *data* for
// interoperability; no Rallly code is involved.
package rallly

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// table is one parsed COPY block.
type table struct {
	cols map[string]int
	rows [][]string // decoded values; SQL NULL is the null sentinel below
}

// null is the in-memory marker for SQL NULL after decoding.
const null = "\x00"

// parseDump extracts every COPY block of a plain-format pg_dump.
// Lines are read with a bufio.Reader rather than a Scanner: a single
// long description or comment would otherwise trip the Scanner's token
// limit and report "token too long" instead of importing.
func parseDump(r io.Reader) (map[string]*table, error) {
	tables := make(map[string]*table)
	br := bufio.NewReaderSize(r, 1<<16)

	var current *table
	for {
		raw, readErr := br.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("read dump: %w", readErr)
		}
		line := strings.TrimRight(raw, "\r\n")
		// A trailing newline yields one empty line with no data: skip it,
		// but keep genuine empty lines inside the file.
		if line != "" || readErr == nil {
			if err := appendLine(tables, &current, line); err != nil {
				return nil, err
			}
		}
		if readErr != nil {
			return tables, nil
		}
	}
}

// appendLine feeds one dump line to the block currently being read,
// opening and closing COPY blocks as they appear.
func appendLine(tables map[string]*table, current **table, line string) error {
	if *current == nil {
		if name, cols, ok := parseCopyHeader(line); ok {
			t := &table{cols: cols}
			tables[name] = t
			*current = t
		}
		return nil
	}
	if line == `\.` {
		*current = nil
		return nil
	}
	fields := strings.Split(line, "\t")
	row := make([]string, len(fields))
	for i, f := range fields {
		v, err := decodeCopyValue(f)
		if err != nil {
			return fmt.Errorf("decode COPY value %q: %w", f, err)
		}
		row[i] = v
	}
	(*current).rows = append((*current).rows, row)
	return nil
}

// requiredTables must be present in any dump worth importing.
var requiredTables = []string{"polls", "options", "participants", "votes"}

// requiredColumns lists, per table, the columns the import cannot do
// without. A missing column does not fail on its own: table.get reports
// "absent" exactly like SQL NULL, which reads as a zero value — every
// poll timed, every timed poll in UTC, every anonymous guest promoted
// to an account, every deleted row resurrected. Those are silent
// corruptions, so the dump's shape is checked up front instead.
// Columns absent from this list are optional: the import has a sound
// fallback for each (a name derived from the email, comments allowed,
// no scheduled event to match).
var requiredColumns = map[string][]string{
	"polls":            {"id", "title", "kind", "status", "deleted", "user_id", "time_zone"},
	"options":          {"id", "poll_id", "start_time", "duration_minutes"},
	"participants":     {"id", "name", "poll_id", "deleted"},
	"votes":            {"participant_id", "option_id", "type"},
	"users":            {"id", "email", "anonymous"},
	"spaces":           {"id", "owner_id"},
	"space_members":    {"space_id", "user_id"},
	"scheduled_events": {"id", "start"},
	"comments":         {"poll_id", "author_name", "content"},
}

// validateSchema reports every missing table and column at once, so an
// unsupported Rallly version is a single legible error rather than a
// half-imported database.
func validateSchema(tables map[string]*table) error {
	var problems []string
	for _, name := range requiredTables {
		if tables[name] == nil {
			problems = append(problems, fmt.Sprintf("table %q is missing", name))
		}
	}
	names := make([]string, 0, len(requiredColumns))
	for name := range requiredColumns {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := tables[name]
		if t == nil {
			continue // optional table; the import skips it
		}
		for _, col := range requiredColumns[name] {
			if _, ok := t.cols[col]; !ok {
				problems = append(problems, fmt.Sprintf("column %s.%s is missing", name, col))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("this does not look like a supported Rallly pg_dump (plain format): %s",
			strings.Join(problems, "; "))
	}
	return nil
}

// parseCopyHeader matches `COPY public.tbl (a, b, "c") FROM stdin;`.
func parseCopyHeader(line string) (string, map[string]int, bool) {
	rest, ok := strings.CutPrefix(line, "COPY public.")
	if !ok {
		return "", nil, false
	}
	name, rest, ok := strings.Cut(rest, " (")
	if !ok {
		return "", nil, false
	}
	colList, _, ok := strings.Cut(rest, ") FROM stdin;")
	if !ok {
		return "", nil, false
	}
	cols := make(map[string]int)
	for i, c := range strings.Split(colList, ", ") {
		cols[strings.Trim(c, `"`)] = i
	}
	return name, cols, true
}

// decodeCopyValue reverses PostgreSQL's COPY text-format escaping.
func decodeCopyValue(s string) (string, error) {
	if s == `\N` {
		return null, nil
	}
	if !strings.ContainsRune(s, '\\') {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			return "", fmt.Errorf("dangling backslash")
		}
		switch e := s[i]; e {
		case '\\':
			b.WriteByte('\\')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'x':
			j := i + 1
			for ; j <= i+2 && j < len(s) && isHex(s[j]); j++ {
			}
			if j == i+1 {
				return "", fmt.Errorf("bad hex escape")
			}
			n, err := strconv.ParseUint(s[i+1:j], 16, 8)
			if err != nil {
				return "", err
			}
			b.WriteByte(byte(n))
			i = j - 1
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i
			for ; j < i+3 && j < len(s) && s[j] >= '0' && s[j] <= '7'; j++ {
			}
			n, err := strconv.ParseUint(s[i:j], 8, 8)
			if err != nil {
				return "", err
			}
			b.WriteByte(byte(n))
			i = j - 1
		default:
			return "", fmt.Errorf("unknown escape \\%c", e)
		}
	}
	return b.String(), nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// get returns a column value; ok is false for SQL NULL or a missing
// column (older dumps may lack newer columns).
func (t *table) get(row []string, col string) (string, bool) {
	i, ok := t.cols[col]
	if !ok || i >= len(row) || row[i] == null {
		return "", false
	}
	return row[i], true
}
