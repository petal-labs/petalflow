package daemon

import (
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/artifact"
)

const (
	// DefaultArtifactMaxSizeBytes is the per-row artifact limit used by serve.
	DefaultArtifactMaxSizeBytes          int64 = 64 << 20
	DefaultArtifactOffloadThresholdBytes int64 = 64 << 10
	DefaultArtifactMaxRunBytes           int64 = 256 << 20
	DefaultArtifactMaxArtifacts                = 1024
	// DefaultArtifactRetention applies an expiration to artifacts without one.
	DefaultArtifactRetention = 30 * 24 * time.Hour
)

// ArtifactsConfig is the optional petalflow.yaml artifact storage policy.
// Omitting the entire section preserves inline-artifact behavior.
type ArtifactsConfig struct {
	OffloadThresholdBytes int64             `yaml:"offload_threshold_bytes"`
	MaxSizeBytes          int64             `yaml:"max_size_bytes"`
	MaxRunBytes           int64             `yaml:"max_run_bytes"`
	MaxArtifacts          int               `yaml:"max_artifacts_per_run"`
	AllowedContentTypes   []string          `yaml:"allowed_content_types,omitempty"`
	Retention             string            `yaml:"retention"`
	TenantRetention       map[string]string `yaml:"tenant_retention,omitempty"`
	WorkflowRetention     map[string]string `yaml:"workflow_retention,omitempty"`
	PruneInterval         string            `yaml:"prune_interval"`
	MaxRetries            int               `yaml:"max_retries"`
	RetryBackoff          string            `yaml:"retry_backoff"`
}

// ParsedArtifactsConfig contains validated artifact storage policy values.
type ParsedArtifactsConfig struct {
	OffloadThresholdBytes int64
	MaxSizeBytes          int64
	MaxRunBytes           int64
	MaxArtifacts          int
	AllowedContentTypes   []string
	Retention             time.Duration
	TenantRetention       map[string]time.Duration
	WorkflowRetention     map[string]time.Duration
	PruneInterval         time.Duration
	MaxRetries            int
	RetryBackoff          time.Duration
}

// Parse validates artifact limits and applies daemon-safe defaults.
func (c *ArtifactsConfig) Parse() (ParsedArtifactsConfig, error) {
	parsed := defaultArtifactsConfig()
	if c == nil {
		return parsed, nil
	}
	if c.MaxSizeBytes < 0 {
		return parsed, errors.New("artifacts.max_size_bytes: must not be negative")
	}
	if c.OffloadThresholdBytes < 0 || c.MaxRunBytes < 0 || c.MaxArtifacts < 0 || c.MaxRetries < 0 {
		return parsed, errors.New("artifacts: thresholds, run limits, counts, and retries must not be negative")
	}
	if c.OffloadThresholdBytes > 0 {
		parsed.OffloadThresholdBytes = c.OffloadThresholdBytes
	}
	if c.MaxRunBytes > 0 {
		parsed.MaxRunBytes = c.MaxRunBytes
	}
	if c.MaxArtifacts > 0 {
		parsed.MaxArtifacts = c.MaxArtifacts
	}
	if c.MaxRetries > 0 {
		parsed.MaxRetries = c.MaxRetries
	}
	if c.MaxSizeBytes > 0 {
		parsed.MaxSizeBytes = c.MaxSizeBytes
		if c.OffloadThresholdBytes == 0 && parsed.OffloadThresholdBytes > parsed.MaxSizeBytes {
			parsed.OffloadThresholdBytes = parsed.MaxSizeBytes
		}
	}
	types, err := parseArtifactContentTypes(c.AllowedContentTypes)
	if err != nil {
		return parsed, err
	}
	parsed.AllowedContentTypes = types
	if parsed.Retention, err = parseArtifactDuration("retention", c.Retention, parsed.Retention, false); err != nil {
		return parsed, err
	}
	if parsed.TenantRetention, err = parseArtifactRetentionOverrides("tenant_retention", c.TenantRetention); err != nil {
		return parsed, err
	}
	if parsed.WorkflowRetention, err = parseArtifactRetentionOverrides("workflow_retention", c.WorkflowRetention); err != nil {
		return parsed, err
	}
	if err := parsed.RetentionPolicy().Validate(); err != nil {
		return parsed, fmt.Errorf("artifacts: %w", err)
	}
	if parsed.PruneInterval, err = parseArtifactDuration("prune_interval", c.PruneInterval, parsed.PruneInterval, true); err != nil {
		return parsed, err
	}
	if parsed.RetryBackoff, err = parseArtifactDuration("retry_backoff", c.RetryBackoff, parsed.RetryBackoff, true); err != nil {
		return parsed, err
	}
	if parsed.OffloadThresholdBytes > parsed.MaxSizeBytes {
		return parsed, errors.New("artifacts.offload_threshold_bytes: must not exceed max_size_bytes")
	}
	if parsed.MaxRunBytes < parsed.MaxSizeBytes {
		return parsed, errors.New("artifacts.max_run_bytes: must be at least max_size_bytes")
	}
	return parsed, nil
}

// RetentionPolicy returns an immutable copy of the parsed precedence policy.
func (c ParsedArtifactsConfig) RetentionPolicy() artifact.RetentionPolicy {
	return artifact.RetentionPolicy{
		Default: c.Retention, Tenants: cloneDurationMap(c.TenantRetention),
		Workflows: cloneDurationMap(c.WorkflowRetention),
	}
}

// LoadArtifactsConfig loads and validates the optional artifacts section.
// A nil result means the section is absent and inline behavior is unchanged.
func LoadArtifactsConfig(configPath string) (*ParsedArtifactsConfig, error) {
	if strings.TrimSpace(configPath) == "" {
		return nil, nil
	}
	cfg, err := loadToolConfig(configPath)
	if err != nil {
		return nil, err
	}
	if cfg.Artifacts == nil {
		return nil, nil
	}
	parsed, err := cfg.Artifacts.Parse()
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func defaultArtifactsConfig() ParsedArtifactsConfig {
	return ParsedArtifactsConfig{
		OffloadThresholdBytes: DefaultArtifactOffloadThresholdBytes,
		MaxSizeBytes:          DefaultArtifactMaxSizeBytes,
		MaxRunBytes:           DefaultArtifactMaxRunBytes,
		MaxArtifacts:          DefaultArtifactMaxArtifacts,
		Retention:             DefaultArtifactRetention,
		PruneInterval:         time.Hour,
		MaxRetries:            2,
		RetryBackoff:          50 * time.Millisecond,
	}
}

func parseArtifactContentTypes(values []string) ([]string, error) {
	parsed := make([]string, 0, len(values))
	for _, value := range values {
		mediaType, params, err := mime.ParseMediaType(value)
		if err != nil || !strings.Contains(mediaType, "/") {
			return nil, fmt.Errorf("artifacts.allowed_content_types: invalid content type %q", value)
		}
		parsed = append(parsed, mime.FormatMediaType(strings.ToLower(mediaType), params))
	}
	return parsed, nil
}

func parseArtifactDuration(field, value string, fallback time.Duration, positive bool) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback, fmt.Errorf("artifacts.%s: %w", field, err)
	}
	if parsed < 0 || (positive && parsed == 0) {
		constraint := "must not be negative"
		if positive {
			constraint = "must be positive"
		}
		return fallback, fmt.Errorf("artifacts.%s: %s", field, constraint)
	}
	return parsed, nil
}

func parseArtifactRetentionOverrides(field string, values map[string]string) (map[string]time.Duration, error) {
	parsed := make(map[string]time.Duration, len(values))
	for id, value := range values {
		retention, err := parseArtifactDuration(field+"."+id, value, 0, false)
		if err != nil {
			return nil, err
		}
		parsed[id] = retention
	}
	return parsed, nil
}

func cloneDurationMap(values map[string]time.Duration) map[string]time.Duration {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]time.Duration, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
