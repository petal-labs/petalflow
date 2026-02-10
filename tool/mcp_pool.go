package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

const (
	defaultMCPPoolSize = 4
	maxMCPPoolSize     = 32
)

type pooledMCPClient struct {
	client  *mcpclient.Client
	closeFn func()
}

type mcpClientPool struct {
	available chan *pooledMCPClient
	all       []*pooledMCPClient
}

func newMCPClientPool(
	ctx context.Context,
	size int,
	build func(context.Context) (*mcpclient.Client, func(), error),
) (*mcpClientPool, error) {
	if build == nil {
		return nil, errors.New("tool: mcp pool build function is nil")
	}
	if size <= 0 {
		size = defaultMCPPoolSize
	}
	if size > maxMCPPoolSize {
		size = maxMCPPoolSize
	}

	pool := &mcpClientPool{
		available: make(chan *pooledMCPClient, size),
		all:       make([]*pooledMCPClient, 0, size),
	}

	for i := 0; i < size; i++ {
		client, closeFn, err := build(ctx)
		if err != nil {
			pool.Close(context.Background())
			return nil, err
		}
		if _, err := client.Initialize(ctx); err != nil {
			closeFn()
			pool.Close(context.Background())
			return nil, fmt.Errorf("tool: mcp adapter initialize failed: %w", err)
		}

		item := &pooledMCPClient{
			client:  client,
			closeFn: closeFn,
		}
		pool.all = append(pool.all, item)
		pool.available <- item
	}

	return pool, nil
}

func (p *mcpClientPool) CallTool(ctx context.Context, params mcpclient.ToolsCallParams) (mcpclient.ToolsCallResult, error) {
	if p == nil {
		return mcpclient.ToolsCallResult{}, errors.New("tool: mcp pool is nil")
	}
	if p.available == nil {
		return mcpclient.ToolsCallResult{}, errors.New("tool: mcp pool is not initialized")
	}

	var item *pooledMCPClient
	select {
	case item = <-p.available:
	case <-ctx.Done():
		return mcpclient.ToolsCallResult{}, ctx.Err()
	}
	defer func() {
		p.available <- item
	}()

	return item.client.CallTool(ctx, params)
}

func (p *mcpClientPool) Close(ctx context.Context) {
	if p == nil {
		return
	}
	for _, item := range p.all {
		if item == nil {
			continue
		}
		if item.closeFn != nil {
			item.closeFn()
		}
	}
}

type mcpClientPoolManager struct {
	mu    sync.Mutex
	pools map[string]*mcpClientPool
}

var sharedMCPClientPools = &mcpClientPoolManager{
	pools: map[string]*mcpClientPool{},
}

func (m *mcpClientPoolManager) getOrCreate(
	ctx context.Context,
	key string,
	build func(context.Context) (*mcpClientPool, error),
) (*mcpClientPool, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("tool: mcp pool key is required")
	}
	if build == nil {
		return nil, errors.New("tool: mcp pool build function is nil")
	}

	m.mu.Lock()
	existing, ok := m.pools[key]
	m.mu.Unlock()
	if ok && existing != nil {
		return existing, nil
	}

	created, err := build(ctx)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if current, exists := m.pools[key]; exists && current != nil {
		created.Close(context.Background())
		return current, nil
	}
	m.pools[key] = created
	return created, nil
}

func mcpPoolKey(regName string, transport MCPTransport, config map[string]string, overlayPath string) (string, error) {
	payload := struct {
		Name      string            `json:"name"`
		Transport MCPTransport      `json:"transport"`
		Config    map[string]string `json:"config,omitempty"`
		Overlay   string            `json:"overlay,omitempty"`
	}{
		Name:      regName,
		Transport: transport,
		Config:    cloneStringMap(config),
		Overlay:   strings.TrimSpace(overlayPath),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func configuredMCPPoolSize() int {
	raw := strings.TrimSpace(os.Getenv("PETALFLOW_MCP_POOL_SIZE"))
	if raw == "" {
		return defaultMCPPoolSize
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return defaultMCPPoolSize
	}
	if value > maxMCPPoolSize {
		return maxMCPPoolSize
	}
	return value
}
