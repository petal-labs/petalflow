package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/artifact"
)

func TestTimeQueryAndErrorHelpers(t *testing.T) {
	want := time.Now().UTC().Truncate(time.Nanosecond)
	for _, value := range []any{want, want.Format(time.RFC3339Nano), []byte(want.Format(time.RFC3339Nano))} {
		got, err := parseTime(value)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseTime(%T) = %v, %v; want %v", value, got, err, want)
		}
	}
	if _, err := parseTime(42); err == nil {
		t.Fatal("parseTime unsupported type error = nil")
	}
	if got := formatTime(want); got != want.Format(databaseTimeFormat) {
		t.Fatalf("formatTime = %q, want fixed-width UTC", got)
	}

	store := &Store{dialect: DialectPostgres}
	if got := store.query("SELECT ?"); got != "SELECT $1" {
		t.Fatalf("Postgres query = %q, want rebound placeholder", got)
	}
	if !errors.Is(classifyError("stat", sql.ErrNoRows), artifact.ErrNotFound) {
		t.Fatal("sql.ErrNoRows did not classify as ErrNotFound")
	}
	if !errors.Is(classifyError("get", context.Canceled), context.Canceled) {
		t.Fatal("context.Canceled was not preserved")
	}
	if !errors.Is(classifyError("get", errors.New("database down")), artifact.ErrUnavailable) {
		t.Fatal("database failure did not classify as ErrUnavailable")
	}
}
