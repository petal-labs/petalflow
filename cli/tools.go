package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/tool"
)

// NewToolsCmd creates the "tools" command group.
func NewToolsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Manage tool registrations",
	}
	cmd.PersistentFlags().String("store-path", "", "Path to SQLite store (default: ~/.petalflow/petalflow.db)")

	cmd.AddCommand(newToolsRegisterCmd())
	cmd.AddCommand(newToolsListCmd())
	cmd.AddCommand(newToolsInspectCmd())
	cmd.AddCommand(newToolsUnregisterCmd())
	cmd.AddCommand(newToolsConfigCmd())
	cmd.AddCommand(newToolsTestCmd())
	cmd.AddCommand(newToolsRefreshCmd())
	cmd.AddCommand(newToolsOverlayCmd())
	cmd.AddCommand(newToolsHealthCmd())

	return cmd
}

func newToolsRegisterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register <name>",
		Short: "Register a tool in the local registry",
		Args:  cobra.ExactArgs(1),
		RunE:  runToolsRegister,
	}
	cmd.Flags().String("type", "", "Tool origin: native | http | stdio | mcp")
	cmd.Flags().String("manifest", "", "Path to manifest JSON")
	cmd.Flags().String("endpoint", "", "Transport endpoint override")
	cmd.Flags().String("command", "", "Transport command override")
	cmd.Flags().StringArray("arg", nil, "Transport argument override (repeatable)")
	cmd.Flags().StringArray("env", nil, "Transport environment override KEY=VALUE (repeatable)")
	cmd.Flags().String("transport-mode", "", "MCP transport mode: stdio | sse")
	cmd.Flags().String("overlay", "", "Path to MCP overlay YAML")
	return cmd
}

func runToolsRegister(cmd *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
	store, err := resolveToolStore(cmd)
	if err != nil {
		return err
	}

	registerOptions, err := resolveRegisterOptions(cmd)
	if err != nil {
		return exitError(exitValidation, "%s", err)
	}

	registration, err := buildRegistrationForRegister(cmd, name, registerOptions)
	if err != nil {
		return err
	}
	ensureRegistrationDefaults(&registration)

	if err := tool.ValidateNewRegistration(cmd.Context(), registration, tool.RegistrationValidationOptions{
		Store: store,
	}); err != nil {
		return formatRegistrationValidationError(err)
	}

	if err := store.Upsert(cmd.Context(), registration); err != nil {
		return exitError(exitRuntime, "saving registration: %v", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Registered tool: %s (%s, status=%s)\n", registration.Name, registration.Origin, registration.Status)
	return nil
}

type toolsRegisterOptions struct {
	manifestPath string
	origin       tool.ToolOrigin
	overlayPath  string
}

func resolveRegisterOptions(cmd *cobra.Command) (toolsRegisterOptions, error) {
	manifestPath, _ := cmd.Flags().GetString("manifest")
	originValue, _ := cmd.Flags().GetString("type")
	overlayPath, _ := cmd.Flags().GetString("overlay")
	origin, err := parseToolOrigin(originValue)
	if err != nil {
		return toolsRegisterOptions{}, err
	}
	return toolsRegisterOptions{
		manifestPath: manifestPath,
		origin:       origin,
		overlayPath:  overlayPath,
	}, nil
}

func buildRegistrationForRegister(cmd *cobra.Command, name string, options toolsRegisterOptions) (tool.ToolRegistration, error) {
	switch {
	case options.manifestPath == "" && options.origin == tool.OriginNative:
		return buildNativeRegistration(name)
	case options.manifestPath == "" && options.origin == tool.OriginMCP:
		return buildMCPRegistration(cmd, name, options.overlayPath)
	default:
		return buildManifestRegistration(cmd, name, options)
	}
}

func buildNativeRegistration(name string) (tool.ToolRegistration, error) {
	builtin, ok := tool.BuiltinRegistration(name)
	if !ok {
		return tool.ToolRegistration{}, exitError(exitValidation, "native tool %q requires --manifest (built-in not found)", name)
	}
	return builtin, nil
}

func buildMCPRegistration(cmd *cobra.Command, name string, overlayPath string) (tool.ToolRegistration, error) {
	mcpTransport, err := buildMCPTransportFromFlags(cmd)
	if err != nil {
		return tool.ToolRegistration{}, exitError(exitValidation, "invalid mcp transport: %v", err)
	}

	registration, err := tool.BuildMCPRegistration(cmd.Context(), name, mcpTransport, map[string]string{}, overlayPath)
	if err != nil {
		return tool.ToolRegistration{}, formatRegistrationValidationError(err)
	}
	return registration, nil
}

func buildManifestRegistration(cmd *cobra.Command, name string, options toolsRegisterOptions) (tool.ToolRegistration, error) {
	if options.manifestPath == "" {
		return tool.ToolRegistration{}, exitError(exitValidation, "--manifest is required for non-native registrations")
	}

	manifest, err := loadManifestFile(options.manifestPath)
	if err != nil {
		return tool.ToolRegistration{}, exitError(exitValidation, "loading manifest: %v", err)
	}
	if strings.TrimSpace(manifest.Tool.Name) == "" {
		manifest.Tool.Name = name
	}
	if manifest.Tool.Name != name {
		return tool.ToolRegistration{}, exitError(exitValidation, "manifest tool.name %q does not match registration name %q", manifest.Tool.Name, name)
	}

	origin := options.origin
	if origin == "" {
		origin = originFromTransport(manifest.Transport.Type)
	}
	if origin == "" {
		return tool.ToolRegistration{}, exitError(exitValidation, "unable to infer tool origin from manifest transport %q", manifest.Transport.Type)
	}

	registration := tool.ToolRegistration{
		Name:     name,
		Manifest: manifest,
		Origin:   origin,
		Status:   tool.StatusReady,
		Enabled:  true,
		Config:   map[string]string{},
	}

	if err := applyTransportOverrides(cmd, &registration.Manifest); err != nil {
		return tool.ToolRegistration{}, exitError(exitValidation, "%s", err)
	}
	if registration.Origin == "" {
		registration.Origin = originFromTransport(registration.Manifest.Transport.Type)
	}
	if registration.Origin == "" {
		return tool.ToolRegistration{}, exitError(exitValidation, "tool origin must be set via --type or manifest transport")
	}
	if registration.Origin == tool.OriginMCP && strings.TrimSpace(options.overlayPath) != "" {
		registration.Overlay = &tool.ToolOverlay{Path: options.overlayPath}
	}

	return registration, nil
}

func ensureRegistrationDefaults(registration *tool.ToolRegistration) {
	if registration.Status == "" {
		registration.Status = tool.StatusReady
	}
	if registration.Config == nil {
		registration.Config = map[string]string{}
	}
}

func newToolsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered and built-in tools",
		RunE:  runToolsList,
	}
}

func runToolsList(cmd *cobra.Command, args []string) error {
	store, err := resolveToolStore(cmd)
	if err != nil {
		return err
	}

	stored, err := store.List(cmd.Context())
	if err != nil {
		return exitError(exitRuntime, "listing tools: %v", err)
	}
	combined := mergeTools(tool.BuiltinRegistrations(), stored)

	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tTYPE\tTRANSPORT\tACTIONS\tSTATUS\tVERSION")
	for _, reg := range combined {
		actions := strings.Join(reg.ActionNames(), ",")
		if actions == "" {
			actions = "-"
		}
		version := strings.TrimSpace(reg.Manifest.Tool.Version)
		if version == "" {
			version = "-"
		}
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			reg.Name,
			displayOrigin(reg),
			displayTransport(reg.Manifest.Transport.Type),
			actions,
			reg.Status,
			version,
		)
	}
	return writer.Flush()
}

func newToolsInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <name>",
		Short: "Inspect a tool registration manifest",
		Args:  cobra.ExactArgs(1),
		RunE:  runToolsInspect,
	}
	cmd.Flags().Bool("actions", false, "Show action schemas only")
	return cmd
}

func runToolsInspect(cmd *cobra.Command, args []string) error {
	store, err := resolveToolStore(cmd)
	if err != nil {
		return err
	}

	name := args[0]
	reg, found, err := resolveRegistration(cmd.Context(), store, name)
	if err != nil {
		return exitError(exitRuntime, "loading tool: %v", err)
	}
	if !found {
		return exitError(exitValidation, "tool %q is not registered", name)
	}

	actionsOnly, _ := cmd.Flags().GetBool("actions")
	out := cmd.OutOrStdout()
	if actionsOnly {
		data, err := json.MarshalIndent(reg.Manifest.Actions, "", "  ")
		if err != nil {
			return exitError(exitRuntime, "encoding actions: %v", err)
		}
		_, _ = out.Write(append(data, '\n'))
		return nil
	}

	view := reg
	view.Config = maskSensitiveConfig(reg.Manifest.Config, reg.Config)
	data, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return exitError(exitRuntime, "encoding registration: %v", err)
	}
	_, _ = out.Write(append(data, '\n'))
	return nil
}

func newToolsUnregisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unregister <name>",
		Short: "Unregister a tool from the local registry",
		Args:  cobra.ExactArgs(1),
		RunE:  runToolsUnregister,
	}
}

func runToolsUnregister(cmd *cobra.Command, args []string) error {
	store, err := resolveToolStore(cmd)
	if err != nil {
		return err
	}
	name := args[0]
	if err := store.Delete(cmd.Context(), name); err != nil {
		return exitError(exitRuntime, "unregistering %q: %v", name, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Unregistered tool: %s\n", name)
	return nil
}

func newToolsConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config <name>",
		Short: "Set or show tool config values",
		Args:  cobra.ExactArgs(1),
		RunE:  runToolsConfig,
	}
	cmd.Flags().StringArray("set", nil, "Set config value KEY=VALUE (repeatable)")
	cmd.Flags().StringArray("set-secret", nil, "Set sensitive config KEY=VALUE (repeatable)")
	cmd.Flags().Bool("show", false, "Show effective config values")
	return cmd
}

func runToolsConfig(cmd *cobra.Command, args []string) error {
	store, err := resolveToolStore(cmd)
	if err != nil {
		return err
	}
	name := args[0]
	reg, found, err := resolveRegistration(cmd.Context(), store, name)
	if err != nil {
		return exitError(exitRuntime, "loading tool: %v", err)
	}
	if !found {
		return exitError(exitValidation, "tool %q is not registered", name)
	}

	if reg.Config == nil {
		reg.Config = make(map[string]string)
	}
	if reg.Manifest.Config == nil {
		reg.Manifest.Config = make(map[string]tool.FieldSpec)
	}

	setValues, _ := cmd.Flags().GetStringArray("set")
	secretValues, _ := cmd.Flags().GetStringArray("set-secret")
	show, _ := cmd.Flags().GetBool("show")

	updated := 0
	for _, value := range setValues {
		key, parsed, err := parseKeyValue(value, true)
		if err != nil {
			return exitError(exitInputParse, "invalid --set value %q: %v", value, err)
		}
		spec := reg.Manifest.Config[key]
		if spec.Sensitive {
			return exitError(exitInputParse, "config %q is sensitive; use --set-secret", key)
		}
		reg.Config[key] = parsed
		updated++
	}

	for _, value := range secretValues {
		key, parsed, err := parseKeyValue(value, false)
		if err != nil {
			return exitError(exitInputParse, "invalid --set-secret value %q: %v", value, err)
		}
		if parsed == "" {
			return exitError(exitInputParse, "secret value for %q is empty", key)
		}

		spec := reg.Manifest.Config[key]
		if spec.Type == "" {
			spec.Type = tool.TypeString
		}
		spec.Sensitive = true
		reg.Manifest.Config[key] = spec

		reg.Config[key] = parsed
		updated++
	}

	if updated > 0 {
		if err := validateConfigOnly(reg); err != nil {
			return formatRegistrationValidationError(err)
		}
		if err := store.Upsert(cmd.Context(), reg); err != nil {
			return exitError(exitRuntime, "saving config: %v", err)
		}
	}

	if show || updated == 0 {
		printToolConfig(cmd, reg)
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Updated %d config value(s) for %s\n", updated, reg.Name)
	return nil
}

func newToolsTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test <name> <action>",
		Short: "Invoke a tool action with test inputs",
		Args:  cobra.ExactArgs(2),
		RunE:  runToolsTest,
	}
	cmd.Flags().StringArray("input", nil, "Input KEY=VALUE pair (repeatable)")
	cmd.Flags().String("input-json", "", "Input object as JSON")
	return cmd
}

func runToolsTest(cmd *cobra.Command, args []string) error {
	store, err := resolveToolStore(cmd)
	if err != nil {
		return err
	}
	name := args[0]
	action := args[1]

	reg, found, err := resolveRegistration(cmd.Context(), store, name)
	if err != nil {
		return exitError(exitRuntime, "loading tool: %v", err)
	}
	if !found {
		return exitError(exitValidation, "tool %q is not registered", name)
	}

	inputs, err := parseToolTestInputs(cmd)
	if err != nil {
		return exitError(exitInputParse, "parsing inputs: %v", err)
	}

	factory := tool.DefaultAdapterFactory{NativeLookup: tool.LookupBuiltinNativeTool}
	adapter, err := factory.New(reg)
	if err != nil {
		return exitError(exitRuntime, "creating adapter: %v", err)
	}
	defer adapter.Close(cmd.Context())

	resp, err := adapter.Invoke(cmd.Context(), tool.InvokeRequest{
		ToolName: name,
		Action:   action,
		Inputs:   inputs,
		Config:   configAsAny(reg.Config),
	})
	if err != nil {
		return exitError(exitRuntime, "tool test failed: %v", err)
	}

	result := map[string]any{
		"success":     true,
		"tool":        name,
		"action":      action,
		"duration_ms": resp.DurationMS,
		"outputs":     resp.Outputs,
	}
	if len(resp.Metadata) > 0 {
		result["metadata"] = resp.Metadata
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return exitError(exitRuntime, "encoding test result: %v", err)
	}

	_, _ = cmd.OutOrStdout().Write(append(data, '\n'))
	return nil
}

func newToolsRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh <name>",
		Short: "Re-discover MCP actions and refresh the stored manifest",
		Args:  cobra.ExactArgs(1),
		RunE:  runToolsRefresh,
	}
}

func runToolsRefresh(cmd *cobra.Command, args []string) error {
	store, err := resolveToolStore(cmd)
	if err != nil {
		return err
	}

	reg, found, err := resolveStoredRegistration(cmd.Context(), store, args[0])
	if err != nil {
		return exitError(exitRuntime, "loading tool: %v", err)
	}
	if !found {
		return exitError(exitValidation, "tool %q is not registered", args[0])
	}
	if reg.Origin != tool.OriginMCP {
		return exitError(exitValidation, "tool %q is not an mcp registration", args[0])
	}

	refreshed, err := tool.RefreshMCPRegistration(cmd.Context(), reg)
	if err != nil {
		return formatRegistrationValidationError(err)
	}
	if err := tool.ValidateNewRegistration(cmd.Context(), refreshed, tool.RegistrationValidationOptions{
		Store:             store,
		AllowExistingName: true,
	}); err != nil {
		return formatRegistrationValidationError(err)
	}
	if err := store.Upsert(cmd.Context(), refreshed); err != nil {
		return exitError(exitRuntime, "saving refreshed registration: %v", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Refreshed MCP tool: %s (%d actions, status=%s)\n", refreshed.Name, len(refreshed.Manifest.Actions), refreshed.Status)
	return nil
}

func newToolsOverlayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overlay <name>",
		Short: "Set or clear overlay for an MCP registration",
		Args:  cobra.ExactArgs(1),
		RunE:  runToolsOverlay,
	}
	cmd.Flags().String("set", "", "Path to overlay YAML (empty to clear)")
	return cmd
}

func runToolsOverlay(cmd *cobra.Command, args []string) error {
	store, err := resolveToolStore(cmd)
	if err != nil {
		return err
	}
	if !cmd.Flags().Changed("set") {
		return exitError(exitInputParse, "--set is required (use empty value to clear)")
	}
	overlayPath, _ := cmd.Flags().GetString("set")

	reg, found, err := resolveStoredRegistration(cmd.Context(), store, args[0])
	if err != nil {
		return exitError(exitRuntime, "loading tool: %v", err)
	}
	if !found {
		return exitError(exitValidation, "tool %q is not registered", args[0])
	}
	if reg.Origin != tool.OriginMCP {
		return exitError(exitValidation, "tool %q is not an mcp registration", args[0])
	}

	if strings.TrimSpace(overlayPath) == "" {
		reg.Overlay = nil
	} else {
		reg.Overlay = &tool.ToolOverlay{Path: overlayPath}
	}

	refreshed, err := tool.RefreshMCPRegistration(cmd.Context(), reg)
	if err != nil {
		return formatRegistrationValidationError(err)
	}
	if err := tool.ValidateNewRegistration(cmd.Context(), refreshed, tool.RegistrationValidationOptions{
		Store:             store,
		AllowExistingName: true,
	}); err != nil {
		return formatRegistrationValidationError(err)
	}
	if err := store.Upsert(cmd.Context(), refreshed); err != nil {
		return exitError(exitRuntime, "saving overlay update: %v", err)
	}

	if refreshed.Overlay == nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Cleared overlay for MCP tool: %s\n", refreshed.Name)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated overlay for MCP tool: %s (%s)\n", refreshed.Name, refreshed.Overlay.Path)
	return nil
}

func newToolsHealthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health [name]",
		Short: "Run health checks for MCP registrations",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runToolsHealth,
	}
	cmd.Flags().Bool("all", false, "Check all registered MCP tools")
	return cmd
}

func runToolsHealth(cmd *cobra.Command, args []string) error {
	store, err := resolveToolStore(cmd)
	if err != nil {
		return err
	}
	all, _ := cmd.Flags().GetBool("all")

	regs, err := store.List(cmd.Context())
	if err != nil {
		return exitError(exitRuntime, "listing tools: %v", err)
	}

	targets := make([]tool.ToolRegistration, 0)
	if all {
		for _, reg := range regs {
			if reg.Origin == tool.OriginMCP {
				targets = append(targets, reg)
			}
		}
	} else {
		if len(args) != 1 {
			return exitError(exitInputParse, "provide <name> or use --all")
		}
		reg, found, err := resolveStoredRegistration(cmd.Context(), store, args[0])
		if err != nil {
			return exitError(exitRuntime, "loading tool: %v", err)
		}
		if !found {
			return exitError(exitValidation, "tool %q is not registered", args[0])
		}
		targets = append(targets, reg)
	}

	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tSTATUS\tLATENCY_MS\tERROR")

	for _, reg := range targets {
		report := tool.EvaluateMCPHealth(cmd.Context(), reg)
		switch report.State {
		case tool.HealthHealthy:
			reg.Status = tool.StatusReady
		case tool.HealthUnhealthy:
			reg.Status = tool.StatusUnhealthy
		default:
			reg.Status = tool.StatusUnverified
		}
		reg.LastHealthCheck = report.CheckedAt
		if err := store.Upsert(cmd.Context(), reg); err != nil {
			return exitError(exitRuntime, "saving health status for %q: %v", reg.Name, err)
		}

		latency := "-"
		if report.LatencyMS > 0 {
			latency = strconv.FormatInt(report.LatencyMS, 10)
		}
		errText := "-"
		if strings.TrimSpace(report.ErrorMessage) != "" {
			errText = report.ErrorMessage
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", reg.Name, reg.Status, latency, errText)
	}

	return writer.Flush()
}

func resolveToolStore(cmd *cobra.Command) (tool.Store, error) {
	storePath, _ := cmd.Flags().GetString("store-path")
	if strings.TrimSpace(storePath) == "" {
		storePath = os.Getenv("PETALFLOW_SQLITE_PATH")
	}
	if strings.TrimSpace(storePath) == "" {
		storePath = os.Getenv("PETALFLOW_TOOLS_STORE_PATH")
	}
	if strings.TrimSpace(storePath) == "" {
		return tool.NewDefaultSQLiteStore()
	}

	dsn := strings.TrimSpace(storePath)
	scope := dsn
	if !strings.HasPrefix(strings.ToLower(dsn), "file:") {
		clean := filepath.Clean(dsn)
		dsn = clean
		scope = clean
	}
	return tool.NewSQLiteStore(tool.SQLiteStoreConfig{
		DSN:   dsn,
		Scope: scope,
	})
}

func resolveRegistration(ctx context.Context, store tool.Store, name string) (tool.ToolRegistration, bool, error) {
	reg, found, err := store.Get(ctx, name)
	if err != nil {
		return tool.ToolRegistration{}, false, err
	}
	if found {
		return reg, true, nil
	}
	builtin, ok := tool.BuiltinRegistration(name)
	return builtin, ok, nil
}

func resolveStoredRegistration(ctx context.Context, store tool.Store, name string) (tool.ToolRegistration, bool, error) {
	reg, found, err := store.Get(ctx, name)
	if err != nil {
		return tool.ToolRegistration{}, false, err
	}
	return reg, found, nil
}

func loadManifestFile(path string) (tool.Manifest, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- CLI path argument.
	if err != nil {
		return tool.Manifest{}, err
	}
	var manifest tool.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return tool.Manifest{}, err
	}
	return manifest, nil
}

func parseToolOrigin(value string) (tool.ToolOrigin, error) {
	switch strings.TrimSpace(value) {
	case "":
		return "", nil
	case string(tool.OriginNative):
		return tool.OriginNative, nil
	case string(tool.OriginHTTP):
		return tool.OriginHTTP, nil
	case string(tool.OriginStdio):
		return tool.OriginStdio, nil
	case string(tool.OriginMCP):
		return tool.OriginMCP, nil
	default:
		return "", fmt.Errorf("unsupported --type %q (use native, http, stdio, mcp)", value)
	}
}

func originFromTransport(transport tool.TransportType) tool.ToolOrigin {
	switch transport {
	case tool.TransportTypeNative:
		return tool.OriginNative
	case tool.TransportTypeHTTP:
		return tool.OriginHTTP
	case tool.TransportTypeStdio:
		return tool.OriginStdio
	case tool.TransportTypeMCP:
		return tool.OriginMCP
	default:
		return ""
	}
}

func applyTransportOverrides(cmd *cobra.Command, manifest *tool.Manifest) error {
	endpoint, _ := cmd.Flags().GetString("endpoint")
	command, _ := cmd.Flags().GetString("command")
	args, _ := cmd.Flags().GetStringArray("arg")
	envPairs, _ := cmd.Flags().GetStringArray("env")

	if endpoint != "" {
		manifest.Transport.Endpoint = endpoint
	}
	if command != "" {
		manifest.Transport.Command = command
	}
	if len(args) > 0 {
		manifest.Transport.Args = slices.Clone(args)
	}
	if len(envPairs) > 0 {
		env := make(map[string]string, len(envPairs))
		for _, pair := range envPairs {
			key, value, err := parseKeyValue(pair, true)
			if err != nil {
				return fmt.Errorf("invalid --env %q: %w", pair, err)
			}
			env[key] = value
		}
		manifest.Transport.Env = env
	}

	if manifest.Transport.Type == "" {
		switch {
		case endpoint != "":
			manifest.Transport.Type = tool.TransportTypeHTTP
		case command != "":
			manifest.Transport.Type = tool.TransportTypeStdio
		}
	}

	return nil
}

func buildMCPTransportFromFlags(cmd *cobra.Command) (tool.MCPTransport, error) {
	transportMode, _ := cmd.Flags().GetString("transport-mode")
	endpoint, _ := cmd.Flags().GetString("endpoint")
	command, _ := cmd.Flags().GetString("command")
	args, _ := cmd.Flags().GetStringArray("arg")
	envPairs, _ := cmd.Flags().GetStringArray("env")

	mode := strings.ToLower(strings.TrimSpace(transportMode))
	if mode == "" {
		switch {
		case strings.TrimSpace(endpoint) != "":
			mode = string(tool.MCPModeSSE)
		case strings.TrimSpace(command) != "":
			mode = string(tool.MCPModeStdio)
		default:
			return tool.MCPTransport{}, fmt.Errorf("--transport-mode is required for mcp tools")
		}
	}

	env := map[string]string{}
	for _, pair := range envPairs {
		key, value, err := parseKeyValue(pair, true)
		if err != nil {
			return tool.MCPTransport{}, fmt.Errorf("invalid --env %q: %w", pair, err)
		}
		env[key] = value
	}

	switch tool.MCPMode(mode) {
	case tool.MCPModeStdio:
		if strings.TrimSpace(command) == "" {
			return tool.MCPTransport{}, fmt.Errorf("--command is required for mcp stdio mode")
		}
		return tool.MCPTransport{
			Mode:    tool.MCPModeStdio,
			Command: command,
			Args:    slices.Clone(args),
			Env:     env,
		}, nil
	case tool.MCPModeSSE:
		if strings.TrimSpace(endpoint) == "" {
			return tool.MCPTransport{}, fmt.Errorf("--endpoint is required for mcp sse mode")
		}
		return tool.MCPTransport{
			Mode:     tool.MCPModeSSE,
			Endpoint: endpoint,
			Args:     slices.Clone(args),
			Env:      env,
		}, nil
	default:
		return tool.MCPTransport{}, fmt.Errorf("unsupported mcp transport mode %q", mode)
	}
}

func displayOrigin(reg tool.ToolRegistration) tool.ToolOrigin {
	if reg.Origin != "" {
		return reg.Origin
	}
	return originFromTransport(reg.Manifest.Transport.Type)
}

func displayTransport(transport tool.TransportType) string {
	switch transport {
	case tool.TransportTypeNative:
		return "in-process"
	case "":
		return "-"
	default:
		return string(transport)
	}
}

func mergeTools(builtins []tool.ToolRegistration, stored []tool.ToolRegistration) []tool.ToolRegistration {
	byName := make(map[string]tool.ToolRegistration, len(builtins)+len(stored))
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

	out := make([]tool.ToolRegistration, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out
}

func maskSensitiveConfig(specs map[string]tool.FieldSpec, values map[string]string) map[string]string {
	return tool.MaskSensitiveConfig(specs, values)
}

func printToolConfig(cmd *cobra.Command, reg tool.ToolRegistration) {
	fmt.Fprintf(cmd.OutOrStdout(), "Tool: %s\n", reg.Name)
	fmt.Fprintln(cmd.OutOrStdout(), "Config:")

	keys := map[string]struct{}{}
	for key := range reg.Manifest.Config {
		keys[key] = struct{}{}
	}
	for key := range reg.Config {
		keys[key] = struct{}{}
	}

	sortedKeys := make([]string, 0, len(keys))
	for key := range keys {
		sortedKeys = append(sortedKeys, key)
	}
	slices.Sort(sortedKeys)

	for _, key := range sortedKeys {
		spec := reg.Manifest.Config[key]
		value := reg.Config[key]
		display := value
		suffix := ""
		if strings.TrimSpace(value) == "" {
			display = "(unset)"
		}
		if spec.Sensitive && strings.TrimSpace(value) != "" {
			display = tool.MaskedSecretValue
			suffix = " (sensitive)"
		} else if spec.Sensitive {
			suffix = " (sensitive)"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s%s\n", key, display, suffix)
	}
}

func parseKeyValue(value string, requireValue bool) (string, string, error) {
	parts := strings.SplitN(value, "=", 2)
	key := strings.TrimSpace(parts[0])
	if key == "" {
		return "", "", errors.New("key is required")
	}
	if len(parts) == 1 {
		if requireValue {
			return "", "", errors.New("value is required")
		}
		return key, "", nil
	}
	return key, parts[1], nil
}

func parseToolTestInputs(cmd *cobra.Command) (map[string]any, error) {
	inputs := map[string]any{}
	rawPairs, _ := cmd.Flags().GetStringArray("input")
	for _, pair := range rawPairs {
		key, value, err := parseKeyValue(pair, true)
		if err != nil {
			return nil, err
		}
		inputs[key] = parsePrimitiveValue(value)
	}

	inputJSON, _ := cmd.Flags().GetString("input-json")
	if strings.TrimSpace(inputJSON) == "" {
		return inputs, nil
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &obj); err != nil {
		return nil, err
	}
	for key, value := range obj {
		inputs[key] = value
	}
	return inputs, nil
}

func parsePrimitiveValue(value string) any {
	if value == "true" {
		return true
	}
	if value == "false" {
		return false
	}
	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f
	}

	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "\"") {
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			return parsed
		}
	}
	return value
}

func configAsAny(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func validateConfigOnly(reg tool.ToolRegistration) error {
	var pipeline tool.Pipeline
	pipeline.AddManifestValidator(tool.SchemaManifestValidator{})
	pipeline.AddManifestValidator(tool.V1TypeSystemValidator{})
	pipeline.AddRegistrationValidator(tool.ConfigCompletenessValidator{})
	pipeline.AddRegistrationValidator(tool.SensitiveFieldValidator{})

	manifestResult := pipeline.ValidateManifest(reg.Manifest)
	registrationResult := pipeline.ValidateRegistration(reg)
	diags := append(manifestResult.Diagnostics, registrationResult.Diagnostics...)
	if !hasToolErrors(diags) {
		return nil
	}

	return &tool.RegistrationValidationError{
		Code:    tool.RegistrationValidationFailedCode,
		Message: "Tool registration failed validation",
		Details: diags,
	}
}

func hasToolErrors(diags []tool.Diagnostic) bool {
	for _, diag := range diags {
		if diag.Severity == tool.SeverityError {
			return true
		}
	}
	return false
}

func formatRegistrationValidationError(err error) error {
	var validationErr *tool.RegistrationValidationError
	if !errors.As(err, &validationErr) {
		return exitError(exitValidation, "%s", err.Error())
	}

	builder := strings.Builder{}
	builder.WriteString(validationErr.Message)
	for _, detail := range validationErr.Details {
		builder.WriteString("\n - ")
		builder.WriteString(detail.Field)
		builder.WriteString(" [")
		builder.WriteString(detail.Code)
		builder.WriteString("] ")
		builder.WriteString(detail.Message)
	}
	return exitError(exitValidation, "%s", builder.String())
}
