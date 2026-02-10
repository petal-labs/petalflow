package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/petal-labs/petalflow/tool"
)

const (
	projectConfigName = "petalflow.yaml"
	homeConfigName    = "config.yaml"
)

// ToolConfigFile is the declarative startup config shape for tool registrations.
type ToolConfigFile struct {
	Tools map[string]ToolDeclaration `yaml:"tools"`
}

// ToolDeclaration defines one tool in petalflow.yaml.
type ToolDeclaration struct {
	Type      string                 `yaml:"type"`
	Manifest  string                 `yaml:"manifest,omitempty"`
	Overlay   string                 `yaml:"overlay,omitempty"`
	Endpoint  string                 `yaml:"endpoint,omitempty"`
	Command   string                 `yaml:"command,omitempty"`
	Args      []string               `yaml:"args,omitempty"`
	Env       map[string]string      `yaml:"env,omitempty"`
	Config    map[string]any         `yaml:"config,omitempty"`
	Enabled   *bool                  `yaml:"enabled,omitempty"`
	Health    *tool.HealthConfig     `yaml:"health,omitempty"`
	Transport ToolDeclarationBinding `yaml:"transport,omitempty"`
}

// ToolDeclarationBinding holds transport-specific declaration fields.
type ToolDeclarationBinding struct {
	Mode     string            `yaml:"mode,omitempty"`
	Endpoint string            `yaml:"endpoint,omitempty"`
	Command  string            `yaml:"command,omitempty"`
	Args     []string          `yaml:"args,omitempty"`
	Env      map[string]string `yaml:"env,omitempty"`
}

// DiscoverToolConfigPath resolves tool config location with first-match semantics.
func DiscoverToolConfigPath(explicitPath string) (string, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false, fmt.Errorf("resolve working directory: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("resolve user home: %w", err)
	}
	return DiscoverToolConfigPathFrom(explicitPath, cwd, homeDir)
}

// DiscoverToolConfigPathFrom is a testable variant of DiscoverToolConfigPath.
func DiscoverToolConfigPathFrom(explicitPath, cwd, homeDir string) (string, bool, error) {
	candidates := make([]string, 0, 3)
	if clean := strings.TrimSpace(explicitPath); clean != "" {
		candidates = append(candidates, filepath.Clean(clean))
	} else {
		candidates = append(candidates, filepath.Join(cwd, projectConfigName))
		candidates = append(candidates, filepath.Join(homeDir, ".petalflow", homeConfigName))
	}

	for i, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			// If explicit path is set, not found is an error.
			if i == 0 && strings.TrimSpace(explicitPath) != "" {
				return "", false, fmt.Errorf("config file %q not found", candidate)
			}
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("checking config path %q: %w", candidate, err)
		}
	}
	return "", false, nil
}

// RegisterToolsFromConfig loads a config file and registers tool declarations.
func RegisterToolsFromConfig(ctx context.Context, service *tool.DaemonToolService, configPath string) ([]tool.ToolRegistration, error) {
	if service == nil {
		return nil, errors.New("daemon: tool service is nil")
	}
	clean := strings.TrimSpace(configPath)
	if clean == "" {
		return nil, nil
	}

	cfg, err := loadToolConfig(clean)
	if err != nil {
		return nil, err
	}
	if len(cfg.Tools) == 0 {
		return nil, nil
	}

	baseDir := filepath.Dir(clean)
	names := make([]string, 0, len(cfg.Tools))
	for name := range cfg.Tools {
		names = append(names, name)
	}
	sort.Strings(names)

	registered := make([]tool.ToolRegistration, 0, len(names))
	for _, name := range names {
		input, err := declarationToRegisterInput(name, cfg.Tools[name], baseDir)
		if err != nil {
			return nil, err
		}
		reg, err := service.Register(ctx, input)
		if err != nil {
			return nil, err
		}
		registered = append(registered, reg)
	}
	return registered, nil
}

func loadToolConfig(path string) (ToolConfigFile, error) {
	// #nosec G304 -- path resolved from explicit local config discovery.
	data, err := os.ReadFile(path)
	if err != nil {
		return ToolConfigFile{}, fmt.Errorf("reading tool config %q: %w", path, err)
	}

	var cfg ToolConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ToolConfigFile{}, fmt.Errorf("parsing tool config %q: %w", path, err)
	}
	return cfg, nil
}

func declarationToRegisterInput(name string, decl ToolDeclaration, configBaseDir string) (tool.RegisterToolInput, error) {
	origin, err := parseToolOrigin(decl.Type)
	if err != nil {
		return tool.RegisterToolInput{}, err
	}

	overlayPath := strings.TrimSpace(expandEnvValue(decl.Overlay))
	if overlayPath != "" {
		overlayPath = resolveConfigRelative(configBaseDir, overlayPath)
	}

	input := tool.RegisterToolInput{
		Name:        strings.TrimSpace(name),
		Origin:      origin,
		Config:      toConfigStrings(decl.Config),
		OverlayPath: overlayPath,
		Enabled:     decl.Enabled,
	}

	switch origin {
	case tool.OriginNative:
		// No additional payload required.
		return input, nil
	case tool.OriginMCP:
		transport, err := declarationToMCPTransport(decl)
		if err != nil {
			return tool.RegisterToolInput{}, err
		}
		input.MCPTransport = &transport
		return input, nil
	case tool.OriginHTTP, tool.OriginStdio:
		manifestPath := strings.TrimSpace(expandEnvValue(decl.Manifest))
		if manifestPath == "" {
			return tool.RegisterToolInput{}, fmt.Errorf("tool %q: manifest is required for %s declarations", name, origin)
		}
		manifestPath = resolveConfigRelative(configBaseDir, manifestPath)
		manifest, err := loadManifestFile(manifestPath)
		if err != nil {
			return tool.RegisterToolInput{}, fmt.Errorf("tool %q: %w", name, err)
		}
		applyManifestDeclarationOverrides(&manifest, decl, origin)
		if decl.Health != nil {
			health := *decl.Health
			manifest.Health = &health
		}
		input.Manifest = &manifest
		return input, nil
	default:
		return tool.RegisterToolInput{}, fmt.Errorf("tool %q: unsupported origin %q", name, origin)
	}
}

func declarationToMCPTransport(decl ToolDeclaration) (tool.MCPTransport, error) {
	mode := strings.ToLower(strings.TrimSpace(expandEnvValue(decl.Transport.Mode)))
	endpoint := strings.TrimSpace(expandEnvValue(decl.Transport.Endpoint))
	command := strings.TrimSpace(expandEnvValue(decl.Transport.Command))

	if mode == "" {
		// Backward compatibility with top-level fields.
		mode = strings.ToLower(strings.TrimSpace(expandEnvValue(decl.Type)))
		if mode == string(tool.OriginMCP) {
			mode = ""
		}
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(expandEnvValue(decl.Endpoint))
	}
	if command == "" {
		command = strings.TrimSpace(expandEnvValue(decl.Command))
	}

	args := decl.Transport.Args
	if len(args) == 0 {
		args = decl.Args
	}
	expandedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		expandedArgs = append(expandedArgs, expandEnvValue(arg))
	}

	env := expandStringMap(decl.Env)
	for key, value := range expandStringMap(decl.Transport.Env) {
		env[key] = value
	}

	if mode == "" {
		switch {
		case endpoint != "":
			mode = string(tool.MCPModeSSE)
		case command != "":
			mode = string(tool.MCPModeStdio)
		}
	}

	switch tool.MCPMode(mode) {
	case tool.MCPModeSSE:
		if endpoint == "" {
			return tool.MCPTransport{}, errors.New("mcp sse transport requires endpoint")
		}
		return tool.MCPTransport{
			Mode:     tool.MCPModeSSE,
			Endpoint: endpoint,
			Args:     expandedArgs,
			Env:      env,
		}, nil
	case tool.MCPModeStdio:
		if command == "" {
			return tool.MCPTransport{}, errors.New("mcp stdio transport requires command")
		}
		return tool.MCPTransport{
			Mode:    tool.MCPModeStdio,
			Command: command,
			Args:    expandedArgs,
			Env:     env,
		}, nil
	default:
		return tool.MCPTransport{}, fmt.Errorf("unsupported mcp mode %q", mode)
	}
}

func applyManifestDeclarationOverrides(manifest *tool.ToolManifest, decl ToolDeclaration, origin tool.ToolOrigin) {
	if manifest == nil {
		return
	}

	endpoint := strings.TrimSpace(expandEnvValue(decl.Endpoint))
	command := strings.TrimSpace(expandEnvValue(decl.Command))
	args := decl.Args
	if len(args) == 0 {
		args = decl.Transport.Args
	}
	env := expandStringMap(decl.Env)
	for key, value := range expandStringMap(decl.Transport.Env) {
		env[key] = value
	}

	switch origin {
	case tool.OriginHTTP:
		manifest.Transport.Type = tool.TransportTypeHTTP
		if endpoint != "" {
			manifest.Transport.Endpoint = endpoint
		}
	case tool.OriginStdio:
		manifest.Transport.Type = tool.TransportTypeStdio
		if command != "" {
			manifest.Transport.Command = command
		}
		if len(args) > 0 {
			manifest.Transport.Args = make([]string, 0, len(args))
			for _, arg := range args {
				manifest.Transport.Args = append(manifest.Transport.Args, expandEnvValue(arg))
			}
		}
		if len(env) > 0 {
			manifest.Transport.Env = env
		}
	}
}

func loadManifestFile(path string) (tool.ToolManifest, error) {
	// #nosec G304 -- path resolved from config file location.
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.ToolManifest{}, fmt.Errorf("reading manifest %q: %w", path, err)
	}
	var manifest tool.ToolManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return tool.ToolManifest{}, fmt.Errorf("parsing manifest %q: %w", path, err)
	}
	return manifest, nil
}

func parseToolOrigin(value string) (tool.ToolOrigin, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "native":
		return tool.OriginNative, nil
	case "mcp":
		return tool.OriginMCP, nil
	case "http":
		return tool.OriginHTTP, nil
	case "stdio":
		return tool.OriginStdio, nil
	default:
		return "", fmt.Errorf("unsupported tool type %q", value)
	}
}

func toConfigStrings(config map[string]any) map[string]string {
	if len(config) == 0 {
		return nil
	}
	out := make(map[string]string, len(config))
	for key, value := range config {
		out[strings.TrimSpace(key)] = expandEnvValue(fmt.Sprint(value))
	}
	return out
}

func expandStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = expandEnvValue(value)
	}
	return out
}

func expandEnvValue(value string) string {
	return os.ExpandEnv(value)
}

func resolveConfigRelative(baseDir, p string) string {
	clean := filepath.Clean(p)
	if filepath.IsAbs(clean) {
		return clean
	}
	return filepath.Join(baseDir, clean)
}
