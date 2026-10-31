package tool

import (
	"context"
	"reflect"
	"testing"
)

func TestSafetyClassificationMatching(t *testing.T) {
	readonly := SafetyClassification{Level: SideEffectReadOnly}
	if readonly.IsDangerous() {
		t.Fatal("readonly is not dangerous")
	}
	if !readonly.MatchesClass(PolicyClassReadonly) {
		t.Fatalf("readonly should match class %q", PolicyClassReadonly)
	}
	if readonly.MatchesClass(PolicyClassSideEffect) {
		t.Fatalf("readonly must not match class %q", PolicyClassSideEffect)
	}

	sideEffect := SafetyClassification{Level: SideEffectSideEffect}
	if !sideEffect.IsDangerous() {
		t.Fatal("side_effect is dangerous")
	}
	if !sideEffect.MatchesClass(PolicyClassSideEffect) {
		t.Fatalf("side_effect should match class %q", PolicyClassSideEffect)
	}

	destructive := SafetyClassification{Level: SideEffectDestructive}
	if !destructive.MatchesClass(PolicyClassSideEffect) || !destructive.MatchesClass(PolicyClassDestructive) {
		t.Fatal("destructive should match side_effect and destructive classes")
	}

	flagged := SafetyClassification{DownstreamWrite: true, Network: true}
	if !flagged.IsDangerous() {
		t.Fatal("downstream_write/network flags are dangerous")
	}
	if !flagged.MatchesClass(PolicyClassDownstreamWrite) || !flagged.MatchesClass(PolicyClassNetwork) {
		t.Fatal("flags should match their classes")
	}
	if flagged.MatchesClass("nonsense") {
		t.Fatal("unknown classes never match")
	}
}

func TestBuiltinClassificationDefaults(t *testing.T) {
	templateReg, ok := BuiltinRegistration("template_render")
	if !ok {
		t.Fatal("template_render builtin missing")
	}
	class := templateReg.EffectiveRegistrationSafety()
	if class.Level != SideEffectReadOnly || class.Network {
		t.Fatalf("template_render classification = %+v, want readonly/no network", class)
	}
	if templateReg.Manifest.EffectiveSafety("render").Level != SideEffectReadOnly {
		t.Fatal("render action should stay readonly")
	}

	httpReg, ok := BuiltinRegistration("http_fetch")
	if !ok {
		t.Fatal("http_fetch builtin missing")
	}
	safety := httpReg.EffectiveRegistrationSafety()
	if !safety.Network || safety.Level != SideEffectReadOnly {
		t.Fatalf("http_fetch tool classification = %+v, want readonly network", safety)
	}
	actionSafety := httpReg.Manifest.EffectiveSafety("fetch")
	if actionSafety.Level != SideEffectSideEffect || !actionSafety.Network {
		t.Fatalf("http_fetch fetch action classification = %+v, want side_effect network", actionSafety)
	}
}

func TestDefaultSafetyForOrigin(t *testing.T) {
	manifest := NewManifest("generic")
	reg := ToolRegistration{Name: "generic", Origin: OriginHTTP, Manifest: manifest}
	if class := reg.EffectiveRegistrationSafety(); class.Level != SideEffectSideEffect || !class.Network {
		t.Fatalf("http default classification = %+v, want side_effect network", class)
	}

	reg.Origin = OriginStdio
	if class := reg.EffectiveRegistrationSafety(); class.Level != SideEffectSideEffect || class.Network {
		t.Fatalf("stdio default classification = %+v, want side_effect no network", class)
	}

	reg.Origin = OriginMCP
	reg.Manifest = NewManifest("mcp")
	reg.Manifest.Transport = NewMCPTransport(MCPTransport{Mode: MCPModeStreamableHTTP, Endpoint: "https://mcp.example.com"})
	if class := reg.EffectiveRegistrationSafety(); class.Level != SideEffectSideEffect || !class.Network {
		t.Fatalf("mcp http default classification = %+v, want side_effect network", class)
	}

	reg.Manifest.Transport = NewMCPTransport(MCPTransport{Mode: MCPModeStdio, Command: "server"})
	if class := reg.EffectiveRegistrationSafety(); class.Network {
		t.Fatalf("mcp stdio default classification = %+v, want no network", class)
	}

	reg.Origin = OriginNative
	if class := reg.EffectiveRegistrationSafety(); class.Level != SideEffectSideEffect {
		t.Fatalf("native default classification = %+v, want side_effect", class)
	}
}

func TestDeclaredClassificationBeatsDefaults(t *testing.T) {
	manifest := NewManifest("explicit")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "https://api.example.com"})
	manifest.Safety = &SafetyClassification{Level: SideEffectReadOnly}
	reg := ToolRegistration{Name: "explicit", Origin: OriginHTTP, Manifest: manifest}

	if class := reg.EffectiveRegistrationSafety(); class.Level != SideEffectReadOnly || class.Network {
		t.Fatalf("declared classification was overridden by defaults: %+v", class)
	}
}

func TestEffectiveSafetyActionOverride(t *testing.T) {
	manifest := NewManifest("mixed")
	manifest.Safety = &SafetyClassification{Level: SideEffectReadOnly}
	manifest.Actions["list"] = ActionSpec{}
	manifest.Actions["delete"] = ActionSpec{Safety: &SafetyClassification{
		Level:           SideEffectDestructive,
		Destructive:     true,
		DownstreamWrite: true,
	}}

	if class := manifest.EffectiveSafety("list"); class.Level != SideEffectReadOnly {
		t.Fatalf("list classification = %+v", class)
	}
	deleteClass := manifest.EffectiveSafety("delete")
	if deleteClass.Level != SideEffectDestructive || !deleteClass.Destructive || !deleteClass.DownstreamWrite {
		t.Fatalf("delete classification = %+v", deleteClass)
	}

	if class := manifest.EffectiveSafety("missing"); class.Level != SideEffectReadOnly {
		t.Fatalf("missing action classification = %+v", class)
	}
}

func TestMergeSafetyCannotUnderClassify(t *testing.T) {
	merged := mergeSafety(
		SafetyClassification{Level: SideEffectReadOnly, Network: true},
		SafetyClassification{Destructive: true},
	)
	if !merged.Network {
		t.Fatalf("merge must keep the tool-level network flag: %+v", merged)
	}
	if merged.Level != SideEffectReadOnly {
		t.Fatalf("undeclared action level must not replace the tool level: %+v", merged)
	}

	upgraded := mergeSafety(
		SafetyClassification{Network: true},
		SafetyClassification{Level: SideEffectDestructive, DownstreamWrite: true},
	)
	if upgraded.Level != SideEffectDestructive || !upgraded.DownstreamWrite {
		t.Fatalf("declared action fields must upgrade: %+v", upgraded)
	}
}

func TestSafetyValidation(t *testing.T) {
	manifest := NewManifest("validated")
	manifest.Safety = &SafetyClassification{Level: SideEffectLevel("bogus")}
	diags := ValidateSafetyClassifications(manifest)
	if len(diags) == 0 {
		t.Fatal("expected INVALID_SIDE_EFFECT_LEVEL diagnostic")
	}
	if diags[0].Code != "INVALID_SIDE_EFFECT_LEVEL" {
		t.Fatalf("diagnostic code = %q, want INVALID_SIDE_EFFECT_LEVEL", diags[0].Code)
	}

	conflicting := NewManifest("conflicted")
	conflicting.Safety = &SafetyClassification{Level: SideEffectReadOnly, Destructive: true}
	diags = ValidateSafetyClassifications(conflicting)
	if len(diags) == 0 || diags[0].Code != "CONFLICTING_CLASSIFICATION" {
		t.Fatalf("expected CONFLICTING_CLASSIFICATION, got %+v", diags)
	}

	clean := NewManifest("clean")
	clean.Safety = &SafetyClassification{Level: SideEffectSideEffect}
	if diags := ValidateSafetyClassifications(clean); len(diags) != 0 {
		t.Fatalf("valid classification produced diagnostics: %+v", diags)
	}
}

func TestClassificationSurvivesOverlayMerge(t *testing.T) {
	manifest := NewManifest("mcp_server")
	manifest.Transport = NewMCPTransport(MCPTransport{Mode: MCPModeStdio, Command: "server"})
	manifest.Actions["read"] = ActionSpec{MCPToolName: "read"}
	manifest.Safety = &SafetyClassification{
		Level: SideEffectSideEffect,
		Notes: "declared classification",
	}

	overlay := MCPOverlay{
		OverlayVersion:       "1.0",
		DescriptionOverrides: map[string]string{"read": "overridden"},
	}
	merged, diags, err := MergeMCPOverlay(manifest, overlay)
	if err != nil {
		t.Fatalf("MergeMCPOverlay(): %v (diags %+v)", err, diags)
	}
	if merged.Safety == nil {
		t.Fatal("overlay merge dropped the tool classification")
	}
	if !reflect.DeepEqual(*merged.Safety, *manifest.Safety) {
		t.Fatalf("merged classification = %+v, want %+v", *merged.Safety, *manifest.Safety)
	}
	if merged.Actions["read"].Description != "overridden" {
		t.Fatalf("sanity: overlay never applied: %q", merged.Actions["read"].Description)
	}
}

func TestStrippedRegistrationFallsBackToDefaults(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:               store,
		ReachabilityChecker: stubReachabilityChecker{},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}
	ctx := context.Background()

	// A manifest without classification: conservative defaults apply.
	stripped := NewManifest("secret_tool")
	stripped.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "https://api.example.com"})
	stripped.Actions["call"] = ActionSpec{}
	reg, err := service.Register(ctx, RegisterToolInput{
		Name:     "secret_tool",
		Origin:   OriginHTTP,
		Manifest: &stripped,
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if reg.Manifest.Safety == nil || reg.Manifest.Safety.Level != SideEffectSideEffect || !reg.Manifest.Safety.Network {
		t.Fatalf("stripped manifest did not receive origin defaults: %+v", reg.Manifest.Safety)
	}
	if class := reg.EffectiveRegistrationSafety(); class.Level != SideEffectSideEffect {
		t.Fatalf("effective classification = %+v, want side_effect", class)
	}

	// Re-registering a rename cannot shed the classification: defaults
	// reapply for stripped manifests.
	cloned := stripped
	cloned.Tool.Name = "secret_tool_renamed"
	reg, err = service.Register(ctx, RegisterToolInput{
		Name:     "secret_tool_renamed",
		Origin:   OriginHTTP,
		Manifest: &cloned,
	})
	if err != nil {
		t.Fatalf("Register clone() error = %v", err)
	}
	class := reg.EffectiveRegistrationSafety()
	if class.Level != SideEffectSideEffect || !class.Network {
		t.Fatalf("renamed clone classification = %+v, want side_effect network", class)
	}
}
