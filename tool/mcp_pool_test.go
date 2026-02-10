package tool

import (
	"context"
	"os"
	"sync"
	"testing"
)

func TestNewMCPAdapterReusesSharedPool(t *testing.T) {
	original := sharedMCPClientPools
	sharedMCPClientPools = &mcpClientPoolManager{pools: map[string]*mcpClientPool{}}
	t.Cleanup(func() {
		sharedMCPClientPools = original
	})
	t.Setenv("PETALFLOW_MCP_POOL_SIZE", "2")

	reg := Registration{
		Name:   "shared_pool_test",
		Origin: OriginMCP,
		Manifest: Manifest{
			Schema:          SchemaToolV1,
			ManifestVersion: ManifestVersionV1,
			Tool: ToolMetadata{
				Name: "shared_pool_test",
			},
			Transport: NewMCPTransport(MCPTransport{
				Mode:    MCPModeStdio,
				Command: os.Args[0],
				Args:    []string{"-test.run=TestToolMCPHelperProcess", "--"},
				Env: map[string]string{
					"GO_WANT_TOOL_MCP_HELPER": "1",
				},
			}),
			Actions: map[string]ActionSpec{
				"list": {
					MCPToolName: "list_s3_objects",
					Outputs: map[string]FieldSpec{
						"keys": {Type: TypeArray, Items: &FieldSpec{Type: TypeString}},
					},
				},
			},
		},
	}

	first, err := NewMCPAdapter(context.Background(), reg)
	if err != nil {
		t.Fatalf("NewMCPAdapter(first) error = %v", err)
	}
	second, err := NewMCPAdapter(context.Background(), reg)
	if err != nil {
		t.Fatalf("NewMCPAdapter(second) error = %v", err)
	}
	if first.pool != second.pool {
		t.Fatal("expected adapters to reuse shared MCP pool")
	}
}

func TestMCPAdapterInvokeConcurrent(t *testing.T) {
	original := sharedMCPClientPools
	sharedMCPClientPools = &mcpClientPoolManager{pools: map[string]*mcpClientPool{}}
	t.Cleanup(func() {
		sharedMCPClientPools = original
	})
	t.Setenv("PETALFLOW_MCP_POOL_SIZE", "3")

	reg := Registration{
		Name:   "concurrency_test",
		Origin: OriginMCP,
		Manifest: Manifest{
			Schema:          SchemaToolV1,
			ManifestVersion: ManifestVersionV1,
			Tool: ToolMetadata{
				Name: "concurrency_test",
			},
			Transport: NewMCPTransport(MCPTransport{
				Mode:    MCPModeStdio,
				Command: os.Args[0],
				Args:    []string{"-test.run=TestToolMCPHelperProcess", "--"},
				Env: map[string]string{
					"GO_WANT_TOOL_MCP_HELPER": "1",
				},
				Retry: RetryPolicy{
					MaxAttempts: 2,
					BackoffMS:   0,
				},
			}),
			Actions: map[string]ActionSpec{
				"list": {
					MCPToolName: "list_s3_objects",
					Outputs: map[string]FieldSpec{
						"keys": {Type: TypeArray, Items: &FieldSpec{Type: TypeString}},
					},
				},
			},
		},
	}

	adapter, err := NewMCPAdapter(context.Background(), reg)
	if err != nil {
		t.Fatalf("NewMCPAdapter() error = %v", err)
	}
	defer adapter.Close(context.Background())

	const workers = 9
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := adapter.Invoke(context.Background(), InvokeRequest{
				Action: "list",
				Inputs: map[string]any{"bucket": "reports"},
			})
			if err != nil {
				errCh <- err
				return
			}
			if _, ok := resp.Outputs["keys"]; !ok {
				errCh <- errMissingKeys
				return
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent invoke error = %v", err)
		}
	}
}

var errMissingKeys = newToolError(ToolErrorCodeDecodeFailure, "missing keys output", false, nil)
