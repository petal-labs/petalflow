package runtime

import (
	"context"
	"testing"
)

func TestToolMocksContextRoundTrip(t *testing.T) {
	want := ToolMockSet{
		Mode:    ToolMockModeAll,
		Results: map[string]map[string]any{"render": {"rendered": "captured"}},
	}
	ctx := ContextWithToolMocks(context.Background(), want)
	got, ok := ToolMocksFromContext(ctx)
	if !ok {
		t.Fatal("ToolMocksFromContext() ok = false, want true")
	}
	if got.Mode != want.Mode || got.Results["render"]["rendered"] != "captured" {
		t.Fatalf("ToolMocksFromContext() = %#v, want %#v", got, want)
	}

	nilContext := ContextWithToolMocks(nil, want)
	if got, ok := ToolMocksFromContext(nilContext); !ok || got.Mode != want.Mode {
		t.Fatalf("nil context round trip = (%#v, %t), want attached mocks", got, ok)
	}
}

func TestToolMocksFromContextWithoutMocks(t *testing.T) {
	if _, ok := ToolMocksFromContext(context.Background()); ok {
		t.Fatal("ToolMocksFromContext() ok = true without attached mocks, want false")
	}
	if _, ok := ToolMocksFromContext(nil); ok {
		t.Fatal("ToolMocksFromContext(nil) ok = true, want false")
	}
}
