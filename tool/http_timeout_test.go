package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPFetch_TimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()

	old := httpFetchTimeout
	httpFetchTimeout = 50 * time.Millisecond
	defer func() { httpFetchTimeout = old }()

	start := time.Now()
	_, err := httpFetchTool{}.Invoke(context.Background(), "", map[string]any{"url": srv.URL}, nil)
	if err == nil {
		t.Fatal("expected http_fetch to time out against a stalled server, got nil error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("http_fetch took %v, expected it to be bounded by the client timeout", elapsed)
	}
}

func TestCheckHTTP_TimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()

	old := reachabilityHTTPTimeout
	reachabilityHTTPTimeout = 50 * time.Millisecond
	defer func() { reachabilityHTTPTimeout = old }()

	start := time.Now()
	err := DefaultReachabilityChecker{}.CheckHTTP(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected CheckHTTP to time out against a stalled server, got nil error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("CheckHTTP took %v, expected it to be bounded by the client timeout", elapsed)
	}
}
