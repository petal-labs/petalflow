package tool

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type httpClientPool struct {
	mu      sync.Mutex
	clients map[time.Duration]*http.Client
}

var sharedHTTPClientPool = &httpClientPool{
	clients: map[time.Duration]*http.Client{},
}

// Timeouts bounding the tool subsystem's outbound HTTP probes, so a stalled
// endpoint cannot hang a tool call or the (sequential) health-check loop. These
// are vars rather than consts so tests can shorten them.
var (
	httpFetchTimeout        = 30 * time.Second
	reachabilityHTTPTimeout = 10 * time.Second
	defaultMCPHealthTimeout = 5 * time.Second
)

func (p *httpClientPool) client(timeout time.Duration) *http.Client {
	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.clients[timeout]; ok {
		return existing
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	p.clients[timeout] = client
	return client
}
