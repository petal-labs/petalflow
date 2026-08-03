package mcp

import "testing"

func TestNewSSETransport_DefaultClientHasTimeout(t *testing.T) {
	tr, err := NewSSETransport(SSETransportConfig{Endpoint: "http://example.com"})
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	if tr.cfg.Client == nil {
		t.Fatal("expected a default HTTP client")
	}
	if tr.cfg.Client.Timeout <= 0 {
		t.Errorf("default SSE transport client has no timeout (Timeout=%v)", tr.cfg.Client.Timeout)
	}
}
