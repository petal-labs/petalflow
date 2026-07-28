package tool

import (
	"context"
	"path/filepath"
	"testing"
)

// runToolStoreConformance exercises the Store interface contract against a
// store constructed by newStore. It is shared across backend implementations
// (SQLite, PostgreSQL) to guarantee identical behavior.
func runToolStoreConformance(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()

	t.Run("UpsertThenGet", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		reg := ToolRegistration{
			Name:     "s3_fetch",
			Origin:   OriginMCP,
			Manifest: NewManifest("s3_fetch"),
		}

		if err := store.Upsert(ctx, reg); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		got, ok, err := store.Get(ctx, "s3_fetch")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if !ok {
			t.Fatal("Get() found = false, want true")
		}
		if got.Name != "s3_fetch" {
			t.Fatalf("Name = %q, want s3_fetch", got.Name)
		}
	})

	t.Run("GetMissing", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		_, ok, err := store.Get(ctx, "does_not_exist")
		if err != nil {
			t.Fatalf("Get() error = %v, want nil", err)
		}
		if ok {
			t.Fatal("Get() found = true, want false")
		}
	})

	t.Run("UpsertUpdatesPayload", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		reg := ToolRegistration{
			Name:     "alpha",
			Origin:   OriginHTTP,
			Manifest: NewManifest("alpha"),
			Config: map[string]string{
				"region": "us-west-2",
			},
		}
		if err := store.Upsert(ctx, reg); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		reg.Config = map[string]string{
			"region": "us-east-1",
		}
		if err := store.Upsert(ctx, reg); err != nil {
			t.Fatalf("Upsert() update error = %v", err)
		}

		got, ok, err := store.Get(ctx, "alpha")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if !ok {
			t.Fatal("Get() found = false, want true")
		}
		if got.Config["region"] != "us-east-1" {
			t.Fatalf("Config[region] = %q, want us-east-1", got.Config["region"])
		}
	})

	t.Run("ListIsNameSorted", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		regBeta := ToolRegistration{
			Name:     "beta",
			Origin:   OriginStdio,
			Manifest: NewManifest("beta"),
		}
		regAlpha := ToolRegistration{
			Name:     "alpha",
			Origin:   OriginHTTP,
			Manifest: NewManifest("alpha"),
		}
		regGamma := ToolRegistration{
			Name:     "gamma",
			Origin:   OriginNative,
			Manifest: NewManifest("gamma"),
		}

		if err := store.Upsert(ctx, regBeta); err != nil {
			t.Fatalf("Upsert(beta) error = %v", err)
		}
		if err := store.Upsert(ctx, regGamma); err != nil {
			t.Fatalf("Upsert(gamma) error = %v", err)
		}
		if err := store.Upsert(ctx, regAlpha); err != nil {
			t.Fatalf("Upsert(alpha) error = %v", err)
		}

		list, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(list) != 3 {
			t.Fatalf("len(List()) = %d, want 3", len(list))
		}
		if list[0].Name != "alpha" || list[1].Name != "beta" || list[2].Name != "gamma" {
			t.Fatalf("List order = [%s, %s, %s], want [alpha, beta, gamma]",
				list[0].Name, list[1].Name, list[2].Name)
		}
	})

	t.Run("UpsertPreservesRegisteredAtAndDefaultsStatus", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		reg := ToolRegistration{
			Name:     "preserved",
			Origin:   OriginNative,
			Manifest: NewManifest("preserved"),
			// Status and RegisteredAt intentionally left zero to exercise defaulting.
		}
		if err := store.Upsert(ctx, reg); err != nil {
			t.Fatalf("Upsert() initial error = %v", err)
		}

		first, ok, err := store.Get(ctx, "preserved")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if !ok {
			t.Fatal("Get() found = false, want true")
		}
		if first.Status != StatusUnverified {
			t.Fatalf("Status = %q, want %q", first.Status, StatusUnverified)
		}
		if first.RegisteredAt.IsZero() {
			t.Fatal("RegisteredAt should be defaulted to a non-zero time")
		}

		update := ToolRegistration{
			Name:     "preserved",
			Origin:   OriginNative,
			Manifest: NewManifest("preserved"),
			// RegisteredAt left zero again; should be preserved from existing row.
		}
		if err := store.Upsert(ctx, update); err != nil {
			t.Fatalf("Upsert() update error = %v", err)
		}

		second, ok, err := store.Get(ctx, "preserved")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if !ok {
			t.Fatal("Get() found = false, want true")
		}
		if !second.RegisteredAt.Equal(first.RegisteredAt) {
			t.Fatalf("RegisteredAt = %v, want preserved %v", second.RegisteredAt, first.RegisteredAt)
		}
		if second.Status != StatusUnverified {
			t.Fatalf("Status = %q, want %q", second.Status, StatusUnverified)
		}
	})

	t.Run("DeleteExistingThenGet", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		reg := ToolRegistration{
			Name:     "deleteme",
			Origin:   OriginNative,
			Manifest: NewManifest("deleteme"),
		}
		if err := store.Upsert(ctx, reg); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		if err := store.Delete(ctx, "deleteme"); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		_, ok, err := store.Get(ctx, "deleteme")
		if err != nil {
			t.Fatalf("Get() after delete error = %v", err)
		}
		if ok {
			t.Fatal("Get() after delete found = true, want false")
		}
	})

	t.Run("DeleteMissingIsNoop", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		if err := store.Delete(ctx, "never_existed"); err != nil {
			t.Fatalf("Delete() missing error = %v, want nil", err)
		}
	})
}

func TestSQLiteToolStoreConformance(t *testing.T) {
	runToolStoreConformance(t, func(t *testing.T) Store {
		t.Helper()
		store, err := NewSQLiteStore(SQLiteStoreConfig{
			DSN:   filepath.Join(t.TempDir(), "tools.db"),
			Scope: "test",
		})
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		return store
	})
}
