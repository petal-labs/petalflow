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
