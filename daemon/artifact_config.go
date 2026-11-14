package daemon

import (
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"
)

const (
	// DefaultArtifactMaxSizeBytes is the per-row artifact limit used by serve.
	DefaultArtifactMaxSizeBytes int64 = 64 << 20
	// DefaultArtifactRetention applies an expiration to artifacts without one.
	DefaultArtifactRetention = 30 * 24 * time.Hour
)

// ArtifactsConfig is the optional petalflow.yaml artifact storage policy.
// Omitting the entire section preserves inline-artifact behavior.
type ArtifactsConfig struct {
	MaxSizeBytes        int64    `yaml:"max_size_bytes"`
	AllowedContentTypes []string `yaml:"allowed_content_types,omitempty"`
	Retention           string   `yaml:"retention"`
	PruneInterval       string   `yaml:"prune_interval"`
}

// ParsedArtifactsConfig contains validated artifact storage policy values.
type ParsedArtifactsConfig struct {
	MaxSizeBytes        int64
	AllowedContentTypes []string
	Retention           time.Duration
	PruneInterval       time.Duration
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
	if c.MaxSizeBytes > 0 {
		parsed.MaxSizeBytes = c.MaxSizeBytes
	}
	types, err := parseArtifactContentTypes(c.AllowedContentTypes)
	if err != nil {
		return parsed, err
	}
	parsed.AllowedContentTypes = types
	if parsed.Retention, err = parseArtifactDuration("retention", c.Retention, parsed.Retention, false); err != nil {
		return parsed, err
	}
	if parsed.PruneInterval, err = parseArtifactDuration("prune_interval", c.PruneInterval, parsed.PruneInterval, true); err != nil {
		return parsed, err
	}
	return parsed, nil
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
		MaxSizeBytes:  DefaultArtifactMaxSizeBytes,
		Retention:     DefaultArtifactRetention,
		PruneInterval: time.Hour,
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
