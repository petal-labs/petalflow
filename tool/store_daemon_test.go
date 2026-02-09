package tool

import (
	"context"
	"errors"
	"testing"
)

type fakeDaemonBackend struct {
	regs map[string]ToolRegistration
}

func newFakeDaemonBackend() *fakeDaemonBackend {
	return &fakeDaemonBackend{
		regs: make(map[string]ToolRegistration),
	}
}

func (f *fakeDaemonBackend) ListToolRegistrations(ctx context.Context) ([]ToolRegistration, error) {
	out := make([]ToolRegistration, 0, len(f.regs))
	for _, reg := range f.regs {
		out = append(out, reg)
	}
	return out, nil
}

func (f *fakeDaemonBackend) GetToolRegistration(ctx context.Context, name string) (ToolRegistration, bool, error) {
	reg, ok := f.regs[name]
	return reg, ok, nil
}

func (f *fakeDaemonBackend) UpsertToolRegistration(ctx context.Context, reg ToolRegistration) error {
	f.regs[reg.Name] = reg
	return nil
}

func (f *fakeDaemonBackend) DeleteToolRegistration(ctx context.Context, name string) error {
	delete(f.regs, name)
	return nil
}

func TestDaemonStoreNilBackend(t *testing.T) {
	store := NewDaemonStore(nil)
	if _, err := store.List(context.Background()); !errors.Is(err, errNilDaemonBackend) {
		t.Fatalf("List() error = %v, want errNilDaemonBackend", err)
	}
	if _, _, err := store.Get(context.Background(), "x"); !errors.Is(err, errNilDaemonBackend) {
		t.Fatalf("Get() error = %v, want errNilDaemonBackend", err)
	}
	if err := store.Upsert(context.Background(), ToolRegistration{Name: "x"}); !errors.Is(err, errNilDaemonBackend) {
		t.Fatalf("Upsert() error = %v, want errNilDaemonBackend", err)
	}
	if err := store.Delete(context.Background(), "x"); !errors.Is(err, errNilDaemonBackend) {
		t.Fatalf("Delete() error = %v, want errNilDaemonBackend", err)
	}
}

func TestDaemonStoreCRUD(t *testing.T) {
	backend := newFakeDaemonBackend()
	store := NewDaemonStore(backend)
	ctx := context.Background()

	reg := ToolRegistration{
		Name:     "http_fetch",
		Origin:   OriginHTTP,
		Manifest: NewManifest("http_fetch"),
		Status:   StatusReady,
	}
	reg.Manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9999"})

	if err := store.Upsert(ctx, reg); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	got, ok, err := store.Get(ctx, "http_fetch")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if got.Name != "http_fetch" {
		t.Fatalf("Get().Name = %q, want http_fetch", got.Name)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(list))
	}

	if err := store.Delete(ctx, "http_fetch"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, ok, err = store.Get(ctx, "http_fetch")
	if err != nil {
		t.Fatalf("Get() after delete error = %v", err)
	}
	if ok {
		t.Fatal("Get() after delete ok = true, want false")
	}
}
