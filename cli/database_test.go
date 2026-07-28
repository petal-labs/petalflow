package cli

import "testing"

func TestDetectBackend(t *testing.T) {
	cases := []struct {
		dsn  string
		want databaseBackend
	}{
		{"postgres://u:p@h:5432/db", backendPostgres},
		{"postgresql://u:p@h:5432/db", backendPostgres},
		{"/var/lib/petalflow/petalflow.db", backendSQLite},
		{"file:/tmp/x.db?cache=shared", backendSQLite},
		{"petalflow.db", backendSQLite},
	}
	for _, c := range cases {
		if got := detectBackend(c.dsn); got != c.want {
			t.Errorf("detectBackend(%q) = %q, want %q", c.dsn, got, c.want)
		}
	}
}
