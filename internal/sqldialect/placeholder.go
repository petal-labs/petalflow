package sqldialect

import (
	"strconv"
	"strings"
)

// Rebind rewrites positional '?' placeholders to PostgreSQL '$N' placeholders.
// '?' characters inside single-quoted string literals are left untouched.
func Rebind(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	inQuote := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			inQuote = !inQuote
			b.WriteByte(c)
		case c == '?' && !inQuote:
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// SQLiteConcurrentWriteOptions returns the DSN with per-connection
// pragmas for the modernc.org/sqlite driver so concurrent writers queue
// for SQLite's write lock instead of failing fast with SQLITE_BUSY:
//
//   - busy_timeout(5000): wait for a locked database.
//   - txlock=immediate: start write transactions as BEGIN IMMEDIATE.
//     A deferred transaction that reads first and then upgrades to
//     write gets SQLITE_BUSY immediately (no retry) if another
//     connection holds the write lock, which is exactly the claim
//     pattern used by workers and schedulers.
//
// PRAGMAs set via database/sql Exec only reach the single pooled
// connection that executed them; DSN parameters apply to every new
// connection.
func SQLiteConcurrentWriteOptions(dsn string) string {
	if strings.Contains(dsn, "_txlock=") {
		return dsn
	}
	extras := []string{"_pragma=busy_timeout(5000)", "_txlock=immediate"}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + strings.Join(extras, "&")
}

// SQLiteBusyTimeout is retained for callers that only need the busy
// timeout pragma.
var SQLiteBusyTimeout = SQLiteConcurrentWriteOptions
