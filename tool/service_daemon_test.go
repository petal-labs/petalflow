package tool

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubAdapter struct {
	response InvokeResponse
	err      error
	lastReq  InvokeRequest
	closed   bool
}

func (a *stubAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	a.lastReq = req
	return a.response, a.err
}

func (a *stubAdapter) Close(ctx context.Context) error {
	a.closed = true
	return nil
}

type stubAdapterFactory struct {
	newFn func(reg Registration) (Adapter, error)
}

func (f stubAdapterFactory) New(reg Registration) (Adapter, error) {
	if f.newFn == nil {
		return nil, errors.New("adapter factory not configured")
	}
	return f.newFn(reg)
}

func TestNewDaemonToolServiceRequiresStore(t *testing.T) {
	_, err := NewDaemonToolService(DaemonToolServiceConfig{})
	if !errors.Is(err, ErrNilServiceStore) {
		t.Fatalf("NewDaemonToolService() error = %v, want ErrNilServiceStore", err)
	}
}

func TestDaemonToolServiceRegisterMCP(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())

	var called bool
	var gotOverlay string
	var gotConfig map[string]string
	var gotTransport MCPTransport

	builder := func(ctx context.Context, name string, transport MCPTransport, config map[string]string, overlayPath string) (Registration, error) {
		called = true
		gotOverlay = overlayPath
		gotConfig = cloneStringMap(config)
		gotTransport = transport

		manifest := NewManifest(name)
		manifest.Transport = NewMCPTransport(transport)
		manifest.Actions["list"] = ActionSpec{
			Inputs: map[string]FieldSpec{
				"bucket": {Type: TypeString, Required: true},
			},
			Outputs: map[string]FieldSpec{
				"count": {Type: TypeInteger},
			},
		}

		return Registration{
			Name:     name,
			Origin:   OriginMCP,
			Manifest: manifest,
			Config:   cloneStringMap(config),
			Status:   StatusReady,
			Enabled:  true,
			Overlay:  &ToolOverlay{Path: overlayPath},
		}, nil
	}

	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:               store,
		MCPBuilder:          builder,
		ReachabilityChecker: stubReachabilityChecker{},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	reg, err := service.Register(context.Background(), RegisterToolInput{
		Name:   "s3_fetch",
		Origin: OriginMCP,
		MCPTransport: &MCPTransport{
			Mode:    MCPModeStdio,
			Command: "s3-mcp",
		},
		Config:      map[string]string{"region": "us-west-2"},
		OverlayPath: "/tmp/s3.overlay.yaml",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if !called {
		t.Fatal("builder was not called")
	}
	if gotOverlay != "/tmp/s3.overlay.yaml" {
		t.Fatalf("overlayPath = %q, want /tmp/s3.overlay.yaml", gotOverlay)
	}
	if gotTransport.Command != "s3-mcp" {
		t.Fatalf("transport command = %q, want s3-mcp", gotTransport.Command)
	}
	if gotConfig["region"] != "us-west-2" {
		t.Fatalf("config region = %q, want us-west-2", gotConfig["region"])
	}
	if reg.Name != "s3_fetch" || reg.Origin != OriginMCP {
		t.Fatalf("registration = %#v, want mcp s3_fetch", reg)
	}
	if reg.Overlay == nil || reg.Overlay.Path != "/tmp/s3.overlay.yaml" {
		t.Fatalf("overlay = %#v, want /tmp/s3.overlay.yaml", reg.Overlay)
	}
}

func TestDaemonToolServiceUpdateConfigAndTestAction(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())

	manifest := NewManifest("http_probe")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	manifest.Actions["execute"] = ActionSpec{
		Inputs: map[string]FieldSpec{
			"query": {Type: TypeString},
		},
		Outputs: map[string]FieldSpec{
			"ok": {Type: TypeBoolean},
		},
	}
	manifest.Config = map[string]FieldSpec{
		"region": {Type: TypeString},
		"token":  {Type: TypeString, Sensitive: true},
	}

	reg := ToolRegistration{
		Name:     "http_probe",
		Origin:   OriginHTTP,
		Manifest: manifest,
		Config: map[string]string{
			"region": "us-east-1",
		},
		Status:  StatusReady,
		Enabled: true,
	}
	if err := store.Upsert(context.Background(), reg); err != nil {
		t.Fatalf("store.Upsert() error = %v", err)
	}

	adapter := &stubAdapter{
		response: InvokeResponse{
			Outputs:    map[string]any{"ok": true},
			DurationMS: 17,
			Metadata:   map[string]any{"source": "stub"},
		},
	}

	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:               store,
		ReachabilityChecker: stubReachabilityChecker{},
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				return adapter, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	updated, err := service.UpdateConfig(context.Background(), "http_probe", ConfigUpdateInput{
		Set:       map[string]string{"region": "us-west-2"},
		SetSecret: map[string]string{"token": "abc123"},
	})
	if err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if updated.Config["region"] != "us-west-2" {
		t.Fatalf("region = %q, want us-west-2", updated.Config["region"])
	}
	if updated.Config["token"] != "abc123" {
		t.Fatalf("token = %q, want abc123", updated.Config["token"])
	}
	if !updated.Manifest.Config["token"].Sensitive {
		t.Fatal("token field should remain sensitive")
	}

	_, err = service.UpdateConfig(context.Background(), "http_probe", ConfigUpdateInput{
		Set: map[string]string{"token": "nope"},
	})
	if err == nil {
		t.Fatal("UpdateConfig() expected error when setting sensitive key via Set")
	}

	result, err := service.TestAction(context.Background(), "http_probe", "execute", map[string]any{
		"query": "ping",
	})
	if err != nil {
		t.Fatalf("TestAction() error = %v", err)
	}
	if !result.Success {
		t.Fatal("result.Success = false, want true")
	}
	if result.DurationMS != 17 {
		t.Fatalf("duration = %d, want 17", result.DurationMS)
	}
	if adapter.lastReq.ToolName != "http_probe" || adapter.lastReq.Action != "execute" {
		t.Fatalf("lastReq = %#v, want tool/action", adapter.lastReq)
	}
	if adapter.lastReq.Config["region"] != "us-west-2" {
		t.Fatalf("invoke config region = %v, want us-west-2", adapter.lastReq.Config["region"])
	}
	if adapter.lastReq.Config["token"] != "abc123" {
		t.Fatalf("invoke config token = %v, want abc123", adapter.lastReq.Config["token"])
	}
	if !adapter.closed {
		t.Fatal("adapter should be closed after TestAction")
	}
}

func TestDaemonToolServiceRefreshOverlayEnableDisable(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())

	manifest := NewManifest("s3_fetch")
	manifest.Transport = NewMCPTransport(MCPTransport{
		Mode:    MCPModeStdio,
		Command: "s3-mcp",
	})
	manifest.Actions["list"] = ActionSpec{
		Inputs: map[string]FieldSpec{
			"bucket": {Type: TypeString, Required: true},
		},
		Outputs: map[string]FieldSpec{
			"count": {Type: TypeInteger},
		},
	}

	existing := ToolRegistration{
		Name:     "s3_fetch",
		Origin:   OriginMCP,
		Manifest: manifest,
		Config:   map[string]string{"region": "us-west-2"},
		Status:   StatusReady,
		Enabled:  true,
		Overlay: &ToolOverlay{
			Path: "/tmp/old.overlay.yaml",
		},
	}
	if err := store.Upsert(context.Background(), existing); err != nil {
		t.Fatalf("store.Upsert() error = %v", err)
	}

	var refresherCalls int
	refresher := func(ctx context.Context, reg Registration) (Registration, error) {
		refresherCalls++
		updated := reg
		updated.Manifest.Actions["download"] = ActionSpec{
			Inputs: map[string]FieldSpec{
				"key": {Type: TypeString, Required: true},
			},
			Outputs: map[string]FieldSpec{
				"bytes": {Type: TypeString},
			},
		}
		updated.Status = StatusReady
		return updated, nil
	}

	healthNow := time.Date(2026, 2, 10, 3, 0, 0, 0, time.UTC)
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:               store,
		MCPRefresher:        refresher,
		ReachabilityChecker: stubReachabilityChecker{},
		MCPHealthEvaluator: func(ctx context.Context, reg Registration) HealthReport {
			return HealthReport{
				ToolName:  reg.Name,
				State:     HealthUnhealthy,
				CheckedAt: healthNow,
			}
		},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	refreshed, err := service.Refresh(context.Background(), "s3_fetch")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if _, ok := refreshed.Manifest.Actions["download"]; !ok {
		t.Fatalf("Refresh() actions = %#v, want download action", refreshed.Manifest.Actions)
	}

	withOverlay, err := service.UpdateOverlay(context.Background(), "s3_fetch", "/tmp/new.overlay.yaml")
	if err != nil {
		t.Fatalf("UpdateOverlay() error = %v", err)
	}
	if withOverlay.Overlay == nil || withOverlay.Overlay.Path != "/tmp/new.overlay.yaml" {
		t.Fatalf("overlay = %#v, want /tmp/new.overlay.yaml", withOverlay.Overlay)
	}

	disabled, err := service.SetEnabled(context.Background(), "s3_fetch", false)
	if err != nil {
		t.Fatalf("SetEnabled(false) error = %v", err)
	}
	if disabled.Enabled || disabled.Status != StatusDisabled {
		t.Fatalf("disabled registration = %#v, want enabled=false status=disabled", disabled)
	}

	enabled, err := service.SetEnabled(context.Background(), "s3_fetch", true)
	if err != nil {
		t.Fatalf("SetEnabled(true) error = %v", err)
	}
	if !enabled.Enabled || enabled.Status != StatusUnhealthy {
		t.Fatalf("enabled registration = %#v, want enabled=true status=unhealthy", enabled)
	}
	if !enabled.LastHealthCheck.Equal(healthNow) {
		t.Fatalf("LastHealthCheck = %v, want %v", enabled.LastHealthCheck, healthNow)
	}
	if refresherCalls < 2 {
		t.Fatalf("refresher calls = %d, want >=2", refresherCalls)
	}
}

func TestDaemonToolServiceDeleteNotFound(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	err = service.Delete(context.Background(), "missing")
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("Delete() error = %v, want ErrToolNotFound", err)
	}
}

func TestDaemonToolServiceHealthBuiltin(t *testing.T) {
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store: NewDaemonStore(newFakeDaemonBackend()),
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	reg, report, err := service.Health(context.Background(), "template_render")
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if reg.Name != "template_render" {
		t.Fatalf("reg.Name = %q, want template_render", reg.Name)
	}
	if report.State != HealthHealthy {
		t.Fatalf("report.State = %q, want healthy", report.State)
	}
}

func TestDaemonToolServiceHealthMCPPersistsStatus(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	manifest := NewManifest("s3_fetch")
	manifest.Transport = NewMCPTransport(MCPTransport{
		Mode:    MCPModeStdio,
		Command: "s3-mcp",
	})
	manifest.Actions["list"] = ActionSpec{
		Outputs: map[string]FieldSpec{
			"count": {Type: TypeInteger},
		},
	}
	seed := ToolRegistration{
		Name:     "s3_fetch",
		Origin:   OriginMCP,
		Manifest: manifest,
		Status:   StatusReady,
		Enabled:  true,
	}
	if err := store.Upsert(context.Background(), seed); err != nil {
		t.Fatalf("store.Upsert() error = %v", err)
	}

	checkedAt := time.Date(2026, 2, 10, 4, 0, 0, 0, time.UTC)
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:               store,
		ReachabilityChecker: stubReachabilityChecker{},
		MCPHealthEvaluator: func(ctx context.Context, reg Registration) HealthReport {
			return HealthReport{
				ToolName:  reg.Name,
				State:     HealthUnhealthy,
				CheckedAt: checkedAt,
			}
		},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	reg, report, err := service.Health(context.Background(), "s3_fetch")
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if reg.Status != StatusUnhealthy {
		t.Fatalf("status = %q, want unhealthy", reg.Status)
	}
	if !reg.LastHealthCheck.Equal(checkedAt) {
		t.Fatalf("last health check = %v, want %v", reg.LastHealthCheck, checkedAt)
	}
	if report.State != HealthUnhealthy {
		t.Fatalf("report.State = %q, want unhealthy", report.State)
	}
}
