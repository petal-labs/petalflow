package tool

// Manifest schema constants for the initial tool contract version.
const (
	ManifestVersionV1 = "1.0"
	SchemaToolV1      = "https://petalflow.dev/schemas/tool-manifest/v1.json"
)

// Manifest describes a registered tool independent of tool origin.
type Manifest struct {
	Schema          string                `json:"$schema,omitempty"`
	ManifestVersion string                `json:"manifest_version"`
	Tool            ToolInfo              `json:"tool"`
	Transport       TransportSpec         `json:"transport"`
	Actions         map[string]ActionSpec `json:"actions"`
	Config          map[string]FieldSpec  `json:"config,omitempty"`
	Health          *HealthConfig         `json:"health,omitempty"`
}

// ToolInfo contains display metadata for a tool.
type ToolInfo struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	License     string   `json:"license,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// ActionSpec defines callable behavior on a tool.
type ActionSpec struct {
	Description string               `json:"description,omitempty"`
	Inputs      map[string]FieldSpec `json:"inputs,omitempty"`
	Outputs     map[string]FieldSpec `json:"outputs,omitempty"`
	Idempotent  bool                 `json:"idempotent,omitempty"`
}

// FieldSpec is the v1 field/type descriptor used for inputs, outputs, and config.
type FieldSpec struct {
	Type        string               `json:"type"`
	Required    bool                 `json:"required,omitempty"`
	Description string               `json:"description,omitempty"`
	Default     any                  `json:"default,omitempty"`
	Sensitive   bool                 `json:"sensitive,omitempty"`
	Items       *FieldSpec           `json:"items,omitempty"`
	Properties  map[string]FieldSpec `json:"properties,omitempty"`
}

// TransportSpec describes how PetalFlow invokes the tool.
type TransportSpec struct {
	Type      string         `json:"type"`
	Endpoint  string         `json:"endpoint,omitempty"`
	Command   string         `json:"command,omitempty"`
	Args      []string       `json:"args,omitempty"`
	Env       map[string]any `json:"env,omitempty"`
	Mode      string         `json:"mode,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
	Retry     RetryPolicy    `json:"retry,omitempty"`
}

// RetryPolicy defines adapter retry behavior.
type RetryPolicy struct {
	MaxAttempts    int   `json:"max_attempts,omitempty"`
	BackoffMS      int   `json:"backoff_ms,omitempty"`
	RetryableCodes []int `json:"retryable_codes,omitempty"`
}

// HealthConfig defines optional tool health-check settings.
type HealthConfig struct {
	Endpoint           string `json:"endpoint,omitempty"`
	Method             string `json:"method,omitempty"`
	IntervalSeconds    int    `json:"interval_seconds,omitempty"`
	TimeoutMS          int    `json:"timeout_ms,omitempty"`
	UnhealthyThreshold int    `json:"unhealthy_threshold,omitempty"`
}

// NewManifest returns a manifest pre-populated with v1 schema metadata.
func NewManifest(name string) Manifest {
	return Manifest{
		Schema:          SchemaToolV1,
		ManifestVersion: ManifestVersionV1,
		Tool: ToolInfo{
			Name: name,
		},
		Actions: make(map[string]ActionSpec),
	}
}
