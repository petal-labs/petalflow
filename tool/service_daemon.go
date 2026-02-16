package tool

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

var (
	// ErrNilServiceStore indicates service creation without a backing store.
	ErrNilServiceStore = errors.New("tool: daemon service store is nil")
	// ErrToolNotFound indicates a mutable registration was not found.
	ErrToolNotFound = errors.New("tool: registration not found")
	// ErrToolNotMCP indicates an operation requires an MCP-backed registration.
	ErrToolNotMCP = errors.New("tool: registration is not mcp")
	// ErrToolDisabled indicates invocation attempted on a disabled tool.
	ErrToolDisabled = errors.New("tool: registration is disabled")
)

// MCPRegistrationBuilder builds a registration from MCP transport discovery.
type MCPRegistrationBuilder func(ctx context.Context, name string, transport MCPTransport, config map[string]string, overlayPath string) (Registration, error)

// MCPRegistrationRefresher refreshes an existing MCP registration.
type MCPRegistrationRefresher func(ctx context.Context, existing Registration) (Registration, error)

// MCPHealthEvaluator reports health for MCP registrations.
type MCPHealthEvaluator func(ctx context.Context, reg Registration) HealthReport

// DaemonToolServiceConfig configures daemon service dependencies.
type DaemonToolServiceConfig struct {
	Store               Store
	AdapterFactory      AdapterFactory
	ReachabilityChecker ReachabilityChecker
	MCPBuilder          MCPRegistrationBuilder
	MCPRefresher        MCPRegistrationRefresher
	MCPHealthEvaluator  MCPHealthEvaluator
}

// RegisterToolInput defines a registration request consumed by daemon services.
type RegisterToolInput struct {
	Name         string
	Origin       ToolOrigin
	Manifest     *ToolManifest
	Config       map[string]string
	MCPTransport *MCPTransport
	OverlayPath  string
	Enabled      *bool
}

// UpdateToolInput defines mutable registration fields for daemon updates.
type UpdateToolInput struct {
	Origin       *ToolOrigin
	Manifest     *ToolManifest
	Config       map[string]string
	MCPTransport *MCPTransport
	OverlayPath  *string
	Enabled      *bool
}

// ConfigUpdateInput defines config mutation payload for a registration.
type ConfigUpdateInput struct {
	Set       map[string]string
	SetSecret map[string]string
}

// ToolTestResult is the structured output for service-driven action tests.
type ToolTestResult struct {
	Success    bool           `json:"success"`
	Outputs    map[string]any `json:"outputs,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// ToolListFilter applies optional filtering to list operations.
type ToolListFilter struct {
	Status          Status
	Enabled         *bool
	IncludeBuiltins bool
}

// DaemonToolService provides daemon-oriented tool registry operations.
type DaemonToolService struct {
	store               Store
	adapterFactory      AdapterFactory
	reachabilityChecker ReachabilityChecker
	mcpBuilder          MCPRegistrationBuilder
	mcpRefresher        MCPRegistrationRefresher
	mcpHealthEvaluator  MCPHealthEvaluator
}

// NewDaemonToolService creates a daemon tool service with defaults.
func NewDaemonToolService(cfg DaemonToolServiceConfig) (*DaemonToolService, error) {
	if cfg.Store == nil {
		return nil, ErrNilServiceStore
	}

	adapterFactory := cfg.AdapterFactory
	if adapterFactory == nil {
		adapterFactory = DefaultAdapterFactory{NativeLookup: LookupBuiltinNativeTool}
	}

	mcpBuilder := cfg.MCPBuilder
	if mcpBuilder == nil {
		mcpBuilder = BuildMCPRegistration
	}

	mcpRefresher := cfg.MCPRefresher
	if mcpRefresher == nil {
		mcpRefresher = RefreshMCPRegistration
	}

	mcpHealthEvaluator := cfg.MCPHealthEvaluator
	if mcpHealthEvaluator == nil {
		mcpHealthEvaluator = EvaluateMCPHealth
	}

	return &DaemonToolService{
		store:               cfg.Store,
		adapterFactory:      adapterFactory,
		reachabilityChecker: cfg.ReachabilityChecker,
		mcpBuilder:          mcpBuilder,
		mcpRefresher:        mcpRefresher,
		mcpHealthEvaluator:  mcpHealthEvaluator,
	}, nil
}

// List returns registrations, optionally merged with built-ins and filtered.
func (s *DaemonToolService) List(ctx context.Context, filter ToolListFilter) ([]ToolRegistration, error) {
	regs, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}

	if filter.IncludeBuiltins {
		regs = mergeTools(BuiltinRegistrations(), regs)
	} else {
		regs = cloneRegistrations(regs)
	}

	if filter.Status == "" && filter.Enabled == nil {
		return regs, nil
	}

	filtered := make([]ToolRegistration, 0, len(regs))
	for _, reg := range regs {
		if filter.Status != "" && reg.Status != filter.Status {
			continue
		}
		if filter.Enabled != nil && reg.Enabled != *filter.Enabled {
			continue
		}
		filtered = append(filtered, reg)
	}
	return filtered, nil
}

// Get returns one registration by name, optionally falling back to built-ins.
func (s *DaemonToolService) Get(ctx context.Context, name string, includeBuiltins bool) (ToolRegistration, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ToolRegistration{}, false, nil
	}

	reg, found, err := s.store.Get(ctx, name)
	if err != nil {
		return ToolRegistration{}, false, err
	}
	if found {
		return reg, true, nil
	}

	if includeBuiltins {
		builtin, ok := BuiltinRegistration(name)
		return builtin, ok, nil
	}

	return ToolRegistration{}, false, nil
}

// Register creates and stores a new registration.
func (s *DaemonToolService) Register(ctx context.Context, input RegisterToolInput) (ToolRegistration, error) {
	reg, err := s.registrationFromRegisterInput(ctx, input)
	if err != nil {
		return ToolRegistration{}, err
	}

	if err := ValidateNewRegistration(ctx, reg, RegistrationValidationOptions{
		Store:               s.store,
		ReachabilityChecker: s.reachabilityChecker,
	}); err != nil {
		return ToolRegistration{}, err
	}

	if err := s.store.Upsert(ctx, reg); err != nil {
		return ToolRegistration{}, err
	}
	return s.mustGetStored(ctx, reg.Name)
}

// Update mutates an existing registration and validates the resulting state.
func (s *DaemonToolService) Update(ctx context.Context, name string, input UpdateToolInput) (ToolRegistration, error) {
	current, err := s.getMutableRegistration(ctx, name)
	if err != nil {
		return ToolRegistration{}, err
	}

	next, err := s.registrationFromUpdateInput(ctx, current, input)
	if err != nil {
		return ToolRegistration{}, err
	}

	if err := ValidateNewRegistration(ctx, next, RegistrationValidationOptions{
		Store:               s.store,
		ReachabilityChecker: s.reachabilityChecker,
		AllowExistingName:   true,
	}); err != nil {
		return ToolRegistration{}, err
	}

	if err := s.store.Upsert(ctx, next); err != nil {
		return ToolRegistration{}, err
	}
	return s.mustGetStored(ctx, next.Name)
}

// Delete removes an existing mutable registration.
func (s *DaemonToolService) Delete(ctx context.Context, name string) error {
	regName := strings.TrimSpace(name)
	if regName == "" {
		return fmt.Errorf("%w: %q", ErrToolNotFound, name)
	}

	_, found, err := s.store.Get(ctx, regName)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrToolNotFound, regName)
	}

	return s.store.Delete(ctx, regName)
}

// UpdateConfig updates plain and sensitive config values for one registration.
func (s *DaemonToolService) UpdateConfig(ctx context.Context, name string, input ConfigUpdateInput) (ToolRegistration, error) {
	reg, err := s.getMutableRegistration(ctx, name)
	if err != nil {
		return ToolRegistration{}, err
	}
	if reg.Config == nil {
		reg.Config = map[string]string{}
	}
	if reg.Manifest.Config == nil {
		reg.Manifest.Config = map[string]FieldSpec{}
	}

	for key, value := range input.Set {
		clean := strings.TrimSpace(key)
		if clean == "" {
			return ToolRegistration{}, fmt.Errorf("tool: config key is required")
		}
		spec := reg.Manifest.Config[clean]
		if spec.Sensitive {
			return ToolRegistration{}, fmt.Errorf("tool: config %q is sensitive; use secret update", clean)
		}
		reg.Config[clean] = value
	}

	for key, value := range input.SetSecret {
		clean := strings.TrimSpace(key)
		if clean == "" {
			return ToolRegistration{}, fmt.Errorf("tool: config key is required")
		}
		if strings.TrimSpace(value) == "" {
			return ToolRegistration{}, fmt.Errorf("tool: secret value for %q is empty", clean)
		}
		spec := reg.Manifest.Config[clean]
		if spec.Type == "" {
			spec.Type = TypeString
		}
		spec.Sensitive = true
		reg.Manifest.Config[clean] = spec
		reg.Config[clean] = value
	}

	if err := validateConfigOnlyRegistration(reg); err != nil {
		return ToolRegistration{}, err
	}

	if err := s.store.Upsert(ctx, reg); err != nil {
		return ToolRegistration{}, err
	}
	return s.mustGetStored(ctx, reg.Name)
}

// TestAction invokes an action against one registration with provided inputs.
func (s *DaemonToolService) TestAction(ctx context.Context, name string, action string, inputs map[string]any) (ToolTestResult, error) {
	reg, found, err := s.Get(ctx, name, true)
	if err != nil {
		return ToolTestResult{}, err
	}
	if !found {
		return ToolTestResult{}, fmt.Errorf("%w: %s", ErrToolNotFound, strings.TrimSpace(name))
	}
	if !reg.Enabled || reg.Status == StatusDisabled {
		return ToolTestResult{}, fmt.Errorf("%w: %s", ErrToolDisabled, reg.Name)
	}

	adapter, err := s.adapterFactory.New(reg)
	if err != nil {
		return ToolTestResult{}, err
	}
	defer adapter.Close(ctx)

	response, err := adapter.Invoke(ctx, InvokeRequest{
		ToolName: reg.Name,
		Action:   action,
		Inputs:   cloneAnyMap(inputs),
		Config:   configAsAnyMap(reg.Config),
	})
	if err != nil {
		return ToolTestResult{}, err
	}

	return ToolTestResult{
		Success:    true,
		Outputs:    response.Outputs,
		DurationMS: response.DurationMS,
		Metadata:   response.Metadata,
	}, nil
}

// Health reports current health for a registration and persists status updates.
func (s *DaemonToolService) Health(ctx context.Context, name string) (ToolRegistration, HealthReport, error) {
	regName := strings.TrimSpace(name)
	if regName == "" {
		return ToolRegistration{}, HealthReport{}, fmt.Errorf("%w: %q", ErrToolNotFound, name)
	}

	reg, found, err := s.store.Get(ctx, regName)
	if err != nil {
		return ToolRegistration{}, HealthReport{}, err
	}
	if !found {
		builtin, ok := BuiltinRegistration(regName)
		if !ok {
			return ToolRegistration{}, HealthReport{}, fmt.Errorf("%w: %s", ErrToolNotFound, regName)
		}
		report := HealthReport{
			ToolName:  builtin.Name,
			State:     HealthHealthy,
			CheckedAt: time.Now().UTC(),
		}
		return builtin, report, nil
	}

	report := HealthReport{
		ToolName:     reg.Name,
		State:        HealthUnknown,
		CheckedAt:    time.Now().UTC(),
		FailureCount: reg.HealthFailures,
	}

	switch {
	case !reg.Enabled:
		reg.Status = StatusDisabled
	case reg.Origin != OriginMCP:
		reg.Status = StatusReady
		reg.HealthFailures = 0
		report.State = HealthHealthy
		report.FailureCount = 0
	default:
		report = s.mcpHealthEvaluator(ctx, reg)
		reg.LastHealthCheck = report.CheckedAt
		s.applyHealthOutcome(&reg, &report)
	}

	if err := s.store.Upsert(ctx, reg); err != nil {
		return ToolRegistration{}, HealthReport{}, err
	}
	updated, err := s.mustGetStored(ctx, regName)
	if err != nil {
		return ToolRegistration{}, HealthReport{}, err
	}
	return updated, report, nil
}

// Refresh re-discovers MCP capabilities and updates stored registration.
func (s *DaemonToolService) Refresh(ctx context.Context, name string) (ToolRegistration, error) {
	reg, err := s.getMutableRegistration(ctx, name)
	if err != nil {
		return ToolRegistration{}, err
	}
	if reg.Origin != OriginMCP {
		return ToolRegistration{}, fmt.Errorf("%w: %s", ErrToolNotMCP, reg.Name)
	}

	updated, err := s.mcpRefresher(ctx, reg)
	if err != nil {
		return ToolRegistration{}, err
	}
	updated.Enabled = reg.Enabled
	if !updated.Enabled {
		updated.Status = StatusDisabled
	}

	if err := ValidateNewRegistration(ctx, updated, RegistrationValidationOptions{
		Store:               s.store,
		ReachabilityChecker: s.reachabilityChecker,
		AllowExistingName:   true,
	}); err != nil {
		return ToolRegistration{}, err
	}
	if err := s.store.Upsert(ctx, updated); err != nil {
		return ToolRegistration{}, err
	}
	return s.mustGetStored(ctx, updated.Name)
}

// UpdateOverlay mutates an MCP overlay reference then refreshes discovery.
func (s *DaemonToolService) UpdateOverlay(ctx context.Context, name string, overlayPath string) (ToolRegistration, error) {
	reg, err := s.getMutableRegistration(ctx, name)
	if err != nil {
		return ToolRegistration{}, err
	}
	if reg.Origin != OriginMCP {
		return ToolRegistration{}, fmt.Errorf("%w: %s", ErrToolNotMCP, reg.Name)
	}

	path := strings.TrimSpace(overlayPath)
	if path == "" {
		reg.Overlay = nil
	} else {
		reg.Overlay = &ToolOverlay{Path: path}
	}

	updated, err := s.mcpRefresher(ctx, reg)
	if err != nil {
		return ToolRegistration{}, err
	}
	updated.Enabled = reg.Enabled
	if !updated.Enabled {
		updated.Status = StatusDisabled
	}

	if err := ValidateNewRegistration(ctx, updated, RegistrationValidationOptions{
		Store:               s.store,
		ReachabilityChecker: s.reachabilityChecker,
		AllowExistingName:   true,
	}); err != nil {
		return ToolRegistration{}, err
	}
	if err := s.store.Upsert(ctx, updated); err != nil {
		return ToolRegistration{}, err
	}
	return s.mustGetStored(ctx, updated.Name)
}

// SetEnabled toggles registration availability for validation/invocation.
func (s *DaemonToolService) SetEnabled(ctx context.Context, name string, enabled bool) (ToolRegistration, error) {
	reg, err := s.getMutableRegistration(ctx, name)
	if err != nil {
		return ToolRegistration{}, err
	}

	reg.Enabled = enabled
	if !enabled {
		reg.Status = StatusDisabled
	} else {
		s.restoreStatusForEnabled(ctx, &reg)
	}

	if err := s.store.Upsert(ctx, reg); err != nil {
		return ToolRegistration{}, err
	}
	return s.mustGetStored(ctx, reg.Name)
}

func (s *DaemonToolService) registrationFromRegisterInput(ctx context.Context, input RegisterToolInput) (ToolRegistration, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return ToolRegistration{}, fmt.Errorf("tool: registration name is required")
	}

	origin := resolveRegisterOrigin(input)
	reg, err := s.buildRegistrationFromRegisterInput(ctx, name, origin, input)
	if err != nil {
		return ToolRegistration{}, err
	}

	return finalizeRegistrationEnabledState(reg, input.Enabled), nil
}

func (s *DaemonToolService) registrationFromUpdateInput(ctx context.Context, current ToolRegistration, input UpdateToolInput) (ToolRegistration, error) {
	origin := resolveUpdateOrigin(current, input)

	// MCP updates default to fresh discovery unless a full manifest override is supplied.
	if shouldRebuildMCPRegistration(origin, input) {
		return s.rebuildMCPRegistrationFromUpdate(ctx, current, input)
	}

	next, err := applyNonRebuildUpdate(current, input, origin)
	if err != nil {
		return ToolRegistration{}, err
	}

	return s.finalizeUpdatedRegistration(ctx, current, next), nil
}

func resolveRegisterOrigin(input RegisterToolInput) ToolOrigin {
	origin := input.Origin
	if origin == "" && input.Manifest != nil {
		origin = inferToolOrigin(input.Manifest.Transport.Type)
	}
	return origin
}

func (s *DaemonToolService) buildRegistrationFromRegisterInput(
	ctx context.Context,
	name string,
	origin ToolOrigin,
	input RegisterToolInput,
) (ToolRegistration, error) {
	if input.Manifest == nil {
		return s.buildManifestlessRegistration(ctx, name, origin, input)
	}
	return buildManifestRegistration(name, origin, input)
}

func (s *DaemonToolService) buildManifestlessRegistration(
	ctx context.Context,
	name string,
	origin ToolOrigin,
	input RegisterToolInput,
) (ToolRegistration, error) {
	switch origin {
	case OriginNative:
		builtin, ok := BuiltinRegistration(name)
		if !ok {
			return ToolRegistration{}, fmt.Errorf("tool: native registration %q requires a manifest", name)
		}
		return builtin, nil

	case OriginMCP:
		if input.MCPTransport == nil {
			return ToolRegistration{}, fmt.Errorf("tool: mcp registration %q requires transport", name)
		}
		return s.mcpBuilder(
			ctx,
			name,
			*input.MCPTransport,
			cloneStringMap(input.Config),
			strings.TrimSpace(input.OverlayPath),
		)

	default:
		return ToolRegistration{}, fmt.Errorf("tool: registration %q requires a manifest", name)
	}
}

func buildManifestRegistration(name string, origin ToolOrigin, input RegisterToolInput) (ToolRegistration, error) {
	manifest := *input.Manifest
	if strings.TrimSpace(manifest.Tool.Name) == "" {
		manifest.Tool.Name = name
	}
	if manifest.Tool.Name != name {
		return ToolRegistration{}, fmt.Errorf("tool: manifest tool.name %q does not match %q", manifest.Tool.Name, name)
	}

	if origin == "" {
		origin = inferToolOrigin(manifest.Transport.Type)
	}
	if origin == "" {
		return ToolRegistration{}, fmt.Errorf("tool: unable to infer registration origin for %q", name)
	}

	reg := ToolRegistration{
		Name:     name,
		Origin:   origin,
		Manifest: manifest,
		Config:   cloneStringMap(input.Config),
		Status:   StatusReady,
		Enabled:  true,
	}
	if origin == OriginMCP {
		path := strings.TrimSpace(input.OverlayPath)
		if path != "" {
			reg.Overlay = &ToolOverlay{Path: path}
		}
	}
	return reg, nil
}

func finalizeRegistrationEnabledState(reg ToolRegistration, enabledOverride *bool) ToolRegistration {
	if reg.Config == nil {
		reg.Config = map[string]string{}
	}
	if enabledOverride != nil {
		reg.Enabled = *enabledOverride
	}
	if !reg.Enabled {
		reg.Status = StatusDisabled
	}
	if reg.Status == "" {
		if reg.Enabled {
			reg.Status = StatusReady
		} else {
			reg.Status = StatusDisabled
		}
	}
	return reg
}

func resolveUpdateOrigin(current ToolRegistration, input UpdateToolInput) ToolOrigin {
	origin := current.Origin
	if input.Origin != nil {
		origin = *input.Origin
	}
	if origin == "" {
		origin = inferToolOrigin(current.Manifest.Transport.Type)
	}
	return origin
}

func shouldRebuildMCPRegistration(origin ToolOrigin, input UpdateToolInput) bool {
	return origin == OriginMCP && input.Manifest == nil
}

func (s *DaemonToolService) rebuildMCPRegistrationFromUpdate(
	ctx context.Context,
	current ToolRegistration,
	input UpdateToolInput,
) (ToolRegistration, error) {
	transport, err := resolveMCPUpdateTransport(current, input)
	if err != nil {
		return ToolRegistration{}, err
	}

	config := cloneStringMap(current.Config)
	if input.Config != nil {
		config = cloneStringMap(input.Config)
	}
	overlayPath := resolveUpdatedOverlayPath(current.Overlay, input.OverlayPath)

	updated, err := s.mcpBuilder(ctx, current.Name, transport, config, overlayPath)
	if err != nil {
		return ToolRegistration{}, err
	}
	updated.RegisteredAt = current.RegisteredAt
	updated.Enabled = resolveUpdatedEnabledState(current.Enabled, input.Enabled)
	if !updated.Enabled {
		updated.Status = StatusDisabled
	}
	return updated, nil
}

func resolveMCPUpdateTransport(current ToolRegistration, input UpdateToolInput) (MCPTransport, error) {
	if input.MCPTransport != nil {
		return *input.MCPTransport, nil
	}

	transport, ok := current.Manifest.Transport.AsMCP()
	if !ok {
		return MCPTransport{}, fmt.Errorf("%w: %s", ErrToolNotMCP, current.Name)
	}
	return transport, nil
}

func resolveUpdatedOverlayPath(currentOverlay *ToolOverlay, overlayPath *string) string {
	path := ""
	if currentOverlay != nil {
		path = currentOverlay.Path
	}
	if overlayPath != nil {
		path = strings.TrimSpace(*overlayPath)
	}
	return path
}

func resolveUpdatedEnabledState(current bool, override *bool) bool {
	if override == nil {
		return current
	}
	return *override
}

func applyNonRebuildUpdate(current ToolRegistration, input UpdateToolInput, origin ToolOrigin) (ToolRegistration, error) {
	next := current
	next.Origin = origin

	if input.Manifest != nil {
		next.Manifest = *input.Manifest
	}
	if strings.TrimSpace(next.Manifest.Tool.Name) == "" {
		next.Manifest.Tool.Name = current.Name
	}
	if next.Manifest.Tool.Name != current.Name {
		return ToolRegistration{}, fmt.Errorf("tool: manifest tool.name %q does not match %q", next.Manifest.Tool.Name, current.Name)
	}

	if input.Config != nil {
		next.Config = cloneStringMap(input.Config)
	}
	if next.Config == nil {
		next.Config = map[string]string{}
	}

	if input.OverlayPath != nil {
		path := strings.TrimSpace(*input.OverlayPath)
		if path == "" {
			next.Overlay = nil
		} else {
			next.Overlay = &ToolOverlay{Path: path}
		}
	}

	if input.Enabled != nil {
		next.Enabled = *input.Enabled
	}

	return next, nil
}

func (s *DaemonToolService) finalizeUpdatedRegistration(
	ctx context.Context,
	current ToolRegistration,
	next ToolRegistration,
) ToolRegistration {
	if !next.Enabled {
		next.Status = StatusDisabled
	} else if current.Status == StatusDisabled {
		s.restoreStatusForEnabled(ctx, &next)
	}

	if next.Status == "" {
		if next.Enabled {
			next.Status = StatusReady
		} else {
			next.Status = StatusDisabled
		}
	}

	return next
}

func (s *DaemonToolService) getMutableRegistration(ctx context.Context, name string) (ToolRegistration, error) {
	regName := strings.TrimSpace(name)
	if regName == "" {
		return ToolRegistration{}, fmt.Errorf("%w: %q", ErrToolNotFound, name)
	}

	reg, found, err := s.store.Get(ctx, regName)
	if err != nil {
		return ToolRegistration{}, err
	}
	if !found {
		return ToolRegistration{}, fmt.Errorf("%w: %s", ErrToolNotFound, regName)
	}
	return reg, nil
}

func (s *DaemonToolService) mustGetStored(ctx context.Context, name string) (ToolRegistration, error) {
	reg, found, err := s.store.Get(ctx, name)
	if err != nil {
		return ToolRegistration{}, err
	}
	if !found {
		return ToolRegistration{}, fmt.Errorf("%w: %s", ErrToolNotFound, name)
	}
	return reg, nil
}

func (s *DaemonToolService) restoreStatusForEnabled(ctx context.Context, reg *ToolRegistration) {
	if reg == nil {
		return
	}
	if reg.Origin != OriginMCP {
		reg.Status = StatusReady
		return
	}

	report := s.mcpHealthEvaluator(ctx, *reg)
	reg.LastHealthCheck = report.CheckedAt
	s.applyHealthOutcome(reg, &report)
}

func (s *DaemonToolService) applyHealthOutcome(reg *ToolRegistration, report *HealthReport) {
	if reg == nil || report == nil {
		return
	}

	switch report.State {
	case HealthHealthy:
		reg.HealthFailures = 0
		reg.Status = StatusReady
	case HealthUnhealthy:
		reg.HealthFailures++
		if reg.HealthFailures >= unhealthyThresholdForRegistration(*reg) {
			reg.Status = StatusUnhealthy
		} else {
			reg.Status = StatusUnverified
		}
	default:
		reg.Status = StatusUnverified
	}
	report.FailureCount = reg.HealthFailures
}

func unhealthyThresholdForRegistration(reg Registration) int {
	if reg.Manifest.Health == nil || reg.Manifest.Health.UnhealthyThreshold <= 0 {
		return 1
	}
	return reg.Manifest.Health.UnhealthyThreshold
}

func inferToolOrigin(transport TransportType) ToolOrigin {
	switch transport {
	case TransportTypeNative:
		return OriginNative
	case TransportTypeHTTP:
		return OriginHTTP
	case TransportTypeStdio:
		return OriginStdio
	case TransportTypeMCP:
		return OriginMCP
	default:
		return ""
	}
}

func validateConfigOnlyRegistration(reg ToolRegistration) error {
	var pipeline Pipeline
	pipeline.AddManifestValidator(SchemaManifestValidator{})
	pipeline.AddManifestValidator(V1TypeSystemValidator{})
	pipeline.AddRegistrationValidator(ConfigCompletenessValidator{})
	pipeline.AddRegistrationValidator(SensitiveFieldValidator{})

	manifestResult := pipeline.ValidateManifest(reg.Manifest)
	registrationResult := pipeline.ValidateRegistration(reg)
	diags := append(manifestResult.Diagnostics, registrationResult.Diagnostics...)
	if !hasValidationErrors(diags) {
		return nil
	}

	return &RegistrationValidationError{
		Code:    RegistrationValidationFailedCode,
		Message: "Tool registration failed validation",
		Details: diags,
	}
}

func configAsAnyMap(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func mergeTools(builtins []ToolRegistration, stored []ToolRegistration) []ToolRegistration {
	byName := make(map[string]ToolRegistration, len(builtins)+len(stored))
	for _, reg := range builtins {
		byName[reg.Name] = reg
	}
	for _, reg := range stored {
		byName[reg.Name] = reg
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]ToolRegistration, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out
}
