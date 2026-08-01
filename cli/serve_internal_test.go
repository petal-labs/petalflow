package cli

import (
	"net/http"
	"testing"
	"time"
)

func TestBuildHTTPServer_SetsTimeouts(t *testing.T) {
	cfg := httpServerTimeouts{
		Read:       1 * time.Second,
		Write:      2 * time.Second,
		Idle:       3 * time.Second,
		ReadHeader: 4 * time.Second,
	}

	srv := buildHTTPServer("127.0.0.1:0", http.NotFoundHandler(), cfg)

	if srv.ReadTimeout != cfg.Read {
		t.Errorf("ReadTimeout = %v, want %v", srv.ReadTimeout, cfg.Read)
	}
	if srv.WriteTimeout != cfg.Write {
		t.Errorf("WriteTimeout = %v, want %v", srv.WriteTimeout, cfg.Write)
	}
	if srv.IdleTimeout != cfg.Idle {
		t.Errorf("IdleTimeout = %v, want %v", srv.IdleTimeout, cfg.Idle)
	}
	if srv.ReadHeaderTimeout != cfg.ReadHeader {
		t.Errorf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, cfg.ReadHeader)
	}
}

func TestNewServeCmd_TimeoutFlagDefaults(t *testing.T) {
	cmd := NewServeCmd()

	idle, err := cmd.Flags().GetDuration("idle-timeout")
	if err != nil {
		t.Fatalf("idle-timeout flag: %v", err)
	}
	if idle != 120*time.Second {
		t.Errorf("idle-timeout default = %v, want 120s", idle)
	}

	readHeader, err := cmd.Flags().GetDuration("read-header-timeout")
	if err != nil {
		t.Fatalf("read-header-timeout flag: %v", err)
	}
	if readHeader != 10*time.Second {
		t.Errorf("read-header-timeout default = %v, want 10s", readHeader)
	}
}
