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

func TestNormalizePostgresScope(t *testing.T) {
	t.Run("strips credentials and query params", func(t *testing.T) {
		dsn := "postgres://user:pass@host:5432/db?sslmode=disable"
		want := "host:5432/db"
		if got := normalizePostgresScope(dsn); got != want {
			t.Errorf("normalizePostgresScope(%q) = %q, want %q", dsn, got, want)
		}
	})

	t.Run("stable across benign DSN edits", func(t *testing.T) {
		original := "postgres://user:pass@host:5432/db?sslmode=disable"
		rotated := "postgres://user:newpass@host:5432/db?sslmode=require&connect_timeout=5"

		gotOriginal := normalizePostgresScope(original)
		gotRotated := normalizePostgresScope(rotated)
		if gotOriginal != gotRotated {
			t.Errorf("scope changed across benign DSN edit: %q != %q", gotOriginal, gotRotated)
		}
	})

	t.Run("unparseable DSN returned unchanged", func(t *testing.T) {
		dsn := "postgres://[::not-valid"
		if got := normalizePostgresScope(dsn); got != dsn {
			t.Errorf("normalizePostgresScope(%q) = %q, want unchanged %q", dsn, got, dsn)
		}
	})
}
