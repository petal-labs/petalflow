package hydrate

import (
	"fmt"
	"math"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/nodes/conditional"
	"github.com/petal-labs/petalflow/nodes/conditional/expr"
)

func init() {
	// Register expression validator for graph-level conditional node validation.
	graph.SetExprValidator(expr.ValidateSyntax)
}

// ClientFactory creates a core.LLMClient for a named provider.
// The hydrate package defines this type but never imports iris directly —
// the caller supplies an implementation backed by llmprovider.
type ClientFactory func(providerName string, cfg ProviderConfig) (core.LLMClient, error)

// liveFactoryOptions holds optional dependencies for non-LLM node types.
type liveFactoryOptions struct {
	toolRegistry *core.ToolRegistry
	humanHandler nodes.HumanHandler
}

// LiveNodeOption configures optional dependencies for NewLiveNodeFactory.
type LiveNodeOption func(*liveFactoryOptions)

// WithToolRegistry provides a ToolRegistry so that tool-type nodes resolve to
// real ToolNode instances instead of FuncNode placeholders.
func WithToolRegistry(r *core.ToolRegistry) LiveNodeOption {
	return func(o *liveFactoryOptions) { o.toolRegistry = r }
}

// WithHumanHandler provides a HumanHandler so that human-type nodes resolve to
// real HumanNode instances instead of FuncNode placeholders.
func WithHumanHandler(h nodes.HumanHandler) LiveNodeOption {
	return func(o *liveFactoryOptions) { o.humanHandler = h }
}

// NewLiveNodeFactory returns a NodeFactory that creates executable nodes for
// supported graph node types. Unsupported node types fail fast so wiring
// issues are surfaced during hydration instead of silently no-oping.
func NewLiveNodeFactory(providers ProviderMap, clientFactory ClientFactory, opts ...LiveNodeOption) NodeFactory {
	var options liveFactoryOptions
	for _, o := range opts {
		o(&options)
	}

	// Cache one client per provider name so multiple nodes sharing a provider reuse it.
	clients := make(map[string]core.LLMClient)

	getClient := func(providerName string) (core.LLMClient, error) {
		if c, ok := clients[providerName]; ok {
			return c, nil
		}
		cfg, ok := providers[providerName]
		if !ok {
			return nil, fmt.Errorf("provider %q not configured", providerName)
		}
		c, err := clientFactory(providerName, cfg)
		if err != nil {
			return nil, err
		}
		clients[providerName] = c
		return c, nil
	}

	return func(nd graph.NodeDef) (core.Node, error) {
		switch nd.Type {
		case "llm_prompt":
			return buildLLMNode(nd, getClient)
		case "llm_router":
			return buildLLMRouter(nd, getClient)
		case "rule_router":
			return buildRuleRouter(nd)
		case "filter":
			return buildFilterNode(nd)
		case "transform":
			return buildTransformNode(nd)
		case "gate":
			return buildGateNode(nd)
		case "guardian":
			return buildGuardianNode(nd)
		case "sink":
			return buildSinkNode(nd)
		case "map":
			return nil, fmt.Errorf("node %q: map node hydration requires a mapper binding", nd.ID)
		case "cache":
			return nil, fmt.Errorf("node %q: cache node hydration requires a wrapped node binding", nd.ID)
		case "merge":
			return buildMergeNode(nd)
		case "human":
			return buildHumanNode(nd, options.humanHandler)
		case "conditional":
			return buildConditionalNode(nd)
		case "noop":
			return core.NewNoopNode(nd.ID), nil
		case "func":
			// Graph IR cannot encode arbitrary Go callbacks; this is an explicit no-op.
			return core.NewFuncNode(nd.ID, nil), nil
		case "tool":
			if options.toolRegistry == nil {
				return nil, fmt.Errorf("node %q: tool node requires a tool registry", nd.ID)
			}
			toolName := configString(nd.Config, "tool_name")
			if toolName == "" {
				return nil, fmt.Errorf("node %q: tool node requires config.tool_name", nd.ID)
			}
			tool, ok := options.toolRegistry.Get(toolName)
			if !ok {
				return nil, fmt.Errorf("node %q: tool %q not found in registry", nd.ID, toolName)
			}
			return buildToolNodeWithName(nd, toolName, tool), nil
		default:
			// Check if the type matches a registered tool.
			if options.toolRegistry != nil {
				if tool, ok := options.toolRegistry.Get(nd.Type); ok {
					return buildToolNode(nd, tool), nil
				}
			}
			return nil, fmt.Errorf("node %q: unsupported node type %q", nd.ID, nd.Type)
		}
	}
}

// buildLLMNode extracts config from a NodeDef and returns an LLMNode.
func buildLLMNode(nd graph.NodeDef, getClient func(string) (core.LLMClient, error)) (core.Node, error) {
	providerName, _ := nd.Config["provider"].(string)
	if providerName == "" {
		return nil, fmt.Errorf("node %q: missing \"provider\" in config", nd.ID)
	}

	client, err := getClient(providerName)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", nd.ID, err)
	}

	cfg := nodes.LLMNodeConfig{
		Model:          configString(nd.Config, "model"),
		System:         configString(nd.Config, "system_prompt"),
		PromptTemplate: configString(nd.Config, "prompt_template"),
		OutputKey:      configString(nd.Config, "output_key"),
	}

	if v, ok := configFloat64(nd.Config, "temperature"); ok {
		cfg.Temperature = &v
	}
	if v, ok := configInt(nd.Config, "max_tokens"); ok {
		cfg.MaxTokens = &v
	}

	return nodes.NewLLMNode(nd.ID, client, cfg), nil
}

// buildLLMRouter extracts config from a NodeDef and returns an LLMRouter.
func buildLLMRouter(nd graph.NodeDef, getClient func(string) (core.LLMClient, error)) (core.Node, error) {
	providerName, _ := nd.Config["provider"].(string)
	if providerName == "" {
		return nil, fmt.Errorf("node %q: missing \"provider\" in config", nd.ID)
	}

	client, err := getClient(providerName)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", nd.ID, err)
	}

	cfg := nodes.LLMRouterConfig{
		Model:       configString(nd.Config, "model"),
		System:      configString(nd.Config, "system_prompt"),
		DecisionKey: configString(nd.Config, "decision_key"),
	}

	if v, ok := configFloat64(nd.Config, "temperature"); ok {
		cfg.Temperature = &v
	}

	// Parse allowed_targets map
	if targets, ok := nd.Config["allowed_targets"].(map[string]any); ok {
		cfg.AllowedTargets = make(map[string]string, len(targets))
		for k, v := range targets {
			if s, ok := v.(string); ok {
				cfg.AllowedTargets[k] = s
			}
		}
	}

	return nodes.NewLLMRouter(nd.ID, client, cfg), nil
}

// --- config helpers ---

func configString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// configFloat64 extracts a float64 from config (JSON numbers are float64).
func configFloat64(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

// configInt extracts an int from config, handling JSON float64 → int coercion.
func configInt(m map[string]any, key string) (int, bool) {
	v, ok := m[key].(float64)
	if !ok {
		return 0, false
	}
	// Guard against NaN/Inf and non-integer values
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return int(v), true
}

// configDuration extracts a time.Duration from config.
// Accepts a string (e.g. "30s", "5m") or a float64 interpreted as seconds.
func configDuration(m map[string]any, key string) time.Duration {
	switch v := m[key].(type) {
	case string:
		d, _ := time.ParseDuration(v)
		return d
	case float64:
		return time.Duration(v * float64(time.Second))
	}
	return 0
}

// --- merge / human / tool builders ---

// buildMergeNode creates a MergeNode from a NodeDef.
func buildMergeNode(nd graph.NodeDef) (core.Node, error) {
	cfg := nodes.MergeNodeConfig{
		OutputKey: configString(nd.Config, "output_key"),
	}

	strategy := configString(nd.Config, "strategy")
	switch strategy {
	case "concat":
		cfg.Strategy = nodes.NewConcatMergeStrategy(nodes.ConcatMergeConfig{
			VarName:   configString(nd.Config, "var_name"),
			Separator: configString(nd.Config, "separator"),
		})
	case "best_score":
		higherIsBetter := true
		if v, ok := nd.Config["higher_is_better"].(bool); ok {
			higherIsBetter = v
		}
		cfg.Strategy = nodes.NewBestScoreMergeStrategy(nodes.BestScoreMergeConfig{
			ScoreVar:       configString(nd.Config, "score_var"),
			HigherIsBetter: higherIsBetter,
		})
	default:
		// "json" or empty → JSON merge (the node default)
		cfg.Strategy = nodes.NewJSONMergeStrategy(nodes.JSONMergeConfig{})
	}

	return nodes.NewMergeNode(nd.ID, cfg), nil
}

// buildHumanNode creates a HumanNode from a NodeDef.
// Returns an error if no HumanHandler was provided.
func buildHumanNode(nd graph.NodeDef, handler nodes.HumanHandler) (core.Node, error) {
	if handler == nil {
		return nil, fmt.Errorf("node %q: human node requires a HumanHandler (use WithHumanHandler)", nd.ID)
	}

	cfg := nodes.HumanNodeConfig{
		RequestType: nodes.HumanRequestType(configString(nd.Config, "mode")),
		Prompt:      configString(nd.Config, "prompt"),
		OutputVar:   configString(nd.Config, "output_var"),
		Timeout:     configDuration(nd.Config, "timeout"),
		Handler:     handler,
	}

	return nodes.NewHumanNode(nd.ID, cfg), nil
}

// buildConditionalNode creates a ConditionalNode from a NodeDef.
func buildConditionalNode(nd graph.NodeDef) (core.Node, error) {
	cfg := conditional.Config{
		Default:     configString(nd.Config, "default"),
		PassThrough: true,
		OutputKey:   configString(nd.Config, "output_key"),
	}

	if order := configString(nd.Config, "evaluation_order"); order != "" {
		cfg.EvaluationOrder = order
	}

	if v, ok := nd.Config["pass_through"].(bool); ok {
		cfg.PassThrough = v
	}

	// Parse conditions array from config
	conditionsRaw, _ := nd.Config["conditions"].([]any)
	for _, raw := range conditionsRaw {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cond := conditional.Condition{
			Name:        configMapString(m, "name"),
			Expression:  configMapString(m, "expression"),
			Description: configMapString(m, "description"),
		}
		cfg.Conditions = append(cfg.Conditions, cond)
	}

	return conditional.NewConditionalNode(nd.ID, cfg)
}

func configMapString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func configStringSlice(m map[string]any, key string) ([]string, bool) {
	raw, ok := m[key].([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}

func configMapStringSlice(m map[string]any, key string) ([]string, bool) {
	return configStringSlice(m, key)
}

func configMapFloat64(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

func configMapInt(m map[string]any, key string) (int, bool) {
	v, ok := m[key].(float64)
	if !ok {
		return 0, false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return int(v), true
}

func configMapAnyMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

// buildToolNode creates a ToolNode from a NodeDef and a resolved tool.
func buildToolNode(nd graph.NodeDef, tool core.PetalTool) *nodes.ToolNode {
	cfg := nodes.ToolNodeConfig{
		ToolName:  nd.Type,
		OutputKey: configString(nd.Config, "output_key"),
		Timeout:   configDuration(nd.Config, "timeout"),
	}

	return nodes.NewToolNode(nd.ID, tool, cfg)
}

// buildToolNodeWithName creates a ToolNode from a NodeDef using an explicit tool name.
func buildToolNodeWithName(nd graph.NodeDef, toolName string, tool core.PetalTool) *nodes.ToolNode {
	cfg := nodes.ToolNodeConfig{
		ToolName:  toolName,
		OutputKey: configString(nd.Config, "output_key"),
		Timeout:   configDuration(nd.Config, "timeout"),
	}

	return nodes.NewToolNode(nd.ID, tool, cfg)
}

func buildRuleRouter(nd graph.NodeDef) (core.Node, error) {
	cfg := nodes.RuleRouterConfig{
		DefaultTarget: configString(nd.Config, "default_target"),
		DecisionKey:   configString(nd.Config, "decision_key"),
	}
	if allow, ok := nd.Config["allow_multiple"].(bool); ok {
		cfg.AllowMultiple = allow
	}

	rulesRaw, _ := nd.Config["rules"].([]any)
	for _, raw := range rulesRaw {
		ruleMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rule := nodes.RouteRule{
			Target: configMapString(ruleMap, "target"),
			Reason: configMapString(ruleMap, "reason"),
		}
		condsRaw, _ := ruleMap["conditions"].([]any)
		for _, cRaw := range condsRaw {
			condMap, ok := cRaw.(map[string]any)
			if !ok {
				continue
			}
			cond := nodes.RouteCondition{
				VarPath: configMapString(condMap, "var_path"),
				Op:      nodes.ConditionOp(configMapString(condMap, "op")),
				Value:   condMap["value"],
			}
			if values, ok := condMap["values"].([]any); ok {
				cond.Values = values
			}
			rule.Conditions = append(rule.Conditions, cond)
		}
		cfg.Rules = append(cfg.Rules, rule)
	}

	return nodes.NewRuleRouter(nd.ID, cfg), nil
}

func buildFilterNode(nd graph.NodeDef) (core.Node, error) {
	cfg := nodes.FilterNodeConfig{
		Target:    nodes.FilterTarget(configString(nd.Config, "target")),
		InputVar:  configString(nd.Config, "input_var"),
		OutputVar: configString(nd.Config, "output_var"),
		StatsVar:  configString(nd.Config, "stats_var"),
	}

	filtersRaw, _ := nd.Config["filters"].([]any)
	for _, raw := range filtersRaw {
		filterMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		op := nodes.FilterOp{
			Type:       nodes.FilterOpType(configMapString(filterMap, "type")),
			ScoreField: configMapString(filterMap, "score_field"),
			Order:      configMapString(filterMap, "order"),
			Field:      configMapString(filterMap, "field"),
			Value:      filterMap["value"],
			Pattern:    configMapString(filterMap, "pattern"),
			Keep:       configMapString(filterMap, "keep"),
		}
		if n, ok := configMapInt(filterMap, "n"); ok {
			op.N = n
		}
		if min, ok := configMapFloat64(filterMap, "min"); ok {
			op.Min = &min
		}
		if max, ok := configMapFloat64(filterMap, "max"); ok {
			op.Max = &max
		}
		if includeTypes, ok := configMapStringSlice(filterMap, "include_types"); ok {
			op.IncludeTypes = includeTypes
		}
		if excludeTypes, ok := configMapStringSlice(filterMap, "exclude_types"); ok {
			op.ExcludeTypes = excludeTypes
		}
		cfg.Filters = append(cfg.Filters, op)
	}

	return nodes.NewFilterNode(nd.ID, cfg), nil
}

func buildTransformNode(nd graph.NodeDef) (core.Node, error) {
	cfg := parseTransformConfig(nd.Config)
	return nodes.NewTransformNode(nd.ID, cfg), nil
}

func parseTransformConfig(m map[string]any) nodes.TransformNodeConfig {
	cfg := nodes.TransformNodeConfig{
		Transform:     nodes.TransformType(configString(m, "transform")),
		InputVar:      configString(m, "input_var"),
		OutputVar:     configString(m, "output_var"),
		Template:      configString(m, "template"),
		Format:        configString(m, "format"),
		Separator:     configString(m, "separator"),
		MergeStrategy: configString(m, "merge_strategy"),
	}

	if inputVars, ok := configStringSlice(m, "input_vars"); ok {
		cfg.InputVars = inputVars
	}
	if fields, ok := configStringSlice(m, "fields"); ok {
		cfg.Fields = fields
	}
	if maxDepth, ok := configMapInt(m, "max_depth"); ok {
		cfg.MaxDepth = maxDepth
	}
	if mappingRaw, ok := m["mapping"].(map[string]any); ok {
		mapping := make(map[string]string, len(mappingRaw))
		for k, v := range mappingRaw {
			if s, ok := v.(string); ok {
				mapping[k] = s
			}
		}
		cfg.Mapping = mapping
	}
	if itemRaw, ok := m["item_transform"].(map[string]any); ok {
		itemCfg := parseTransformConfig(itemRaw)
		cfg.ItemTransform = &itemCfg
	}

	return cfg
}

func buildGateNode(nd graph.NodeDef) (core.Node, error) {
	cfg := nodes.GateNodeConfig{
		ConditionVar:   configString(nd.Config, "condition_var"),
		OnFail:         nodes.GateAction(configString(nd.Config, "on_fail")),
		FailMessage:    configString(nd.Config, "fail_message"),
		RedirectNodeID: configString(nd.Config, "redirect_node_id"),
		ResultVar:      configString(nd.Config, "result_var"),
	}
	return nodes.NewGateNode(nd.ID, cfg), nil
}

func buildGuardianNode(nd graph.NodeDef) (core.Node, error) {
	cfg := nodes.GuardianNodeConfig{
		InputVar:           configString(nd.Config, "input_var"),
		OnFail:             nodes.GuardianAction(configString(nd.Config, "on_fail")),
		FailMessage:        configString(nd.Config, "fail_message"),
		RedirectNodeID:     configString(nd.Config, "redirect_node_id"),
		ResultVar:          configString(nd.Config, "result_var"),
		StopOnFirstFailure: false,
	}
	if stop, ok := nd.Config["stop_on_first_failure"].(bool); ok {
		cfg.StopOnFirstFailure = stop
	}

	checksRaw, _ := nd.Config["checks"].([]any)
	for _, raw := range checksRaw {
		checkMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		check := nodes.GuardianCheck{
			Name:         configMapString(checkMap, "name"),
			Type:         nodes.GuardianCheckType(configMapString(checkMap, "type")),
			Field:        configMapString(checkMap, "field"),
			Pattern:      configMapString(checkMap, "pattern"),
			ExpectedType: configMapString(checkMap, "expected_type"),
			Schema:       configMapAnyMap(checkMap, "schema"),
			Message:      configMapString(checkMap, "message"),
		}
		if required, ok := configMapStringSlice(checkMap, "required_fields"); ok {
			check.RequiredFields = required
		}
		if maxLength, ok := configMapInt(checkMap, "max_length"); ok {
			check.MaxLength = maxLength
		}
		if minLength, ok := configMapInt(checkMap, "min_length"); ok {
			check.MinLength = minLength
		}
		if min, ok := configMapFloat64(checkMap, "min"); ok {
			check.Min = &min
		}
		if max, ok := configMapFloat64(checkMap, "max"); ok {
			check.Max = &max
		}
		if allowed, ok := checkMap["allowed_values"].([]any); ok {
			check.AllowedValues = allowed
		}
		if blockPII, ok := checkMap["block_pii"].(bool); ok {
			check.BlockPII = blockPII
		}
		if piiTypes, ok := configMapStringSlice(checkMap, "pii_types"); ok {
			check.PIITypes = make([]nodes.PIIType, 0, len(piiTypes))
			for _, piiType := range piiTypes {
				check.PIITypes = append(check.PIITypes, nodes.PIIType(piiType))
			}
		}
		cfg.Checks = append(cfg.Checks, check)
	}

	return nodes.NewGuardianNode(nd.ID, cfg), nil
}

func buildSinkNode(nd graph.NodeDef) (core.Node, error) {
	cfg := nodes.SinkNodeConfig{
		Template:    configString(nd.Config, "template"),
		ErrorPolicy: nodes.SinkErrorPolicy(configString(nd.Config, "error_policy")),
		ResultVar:   configString(nd.Config, "result_var"),
	}
	if inputVars, ok := configStringSlice(nd.Config, "input_vars"); ok {
		cfg.InputVars = inputVars
	}
	if includeArtifacts, ok := nd.Config["include_artifacts"].(bool); ok {
		cfg.IncludeArtifacts = includeArtifacts
	}
	if includeMessages, ok := nd.Config["include_messages"].(bool); ok {
		cfg.IncludeMessages = includeMessages
	}
	if includeTrace, ok := nd.Config["include_trace"].(bool); ok {
		cfg.IncludeTrace = includeTrace
	}

	sinksRaw, _ := nd.Config["sinks"].([]any)
	for _, raw := range sinksRaw {
		sinkMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		target := nodes.SinkTarget{
			Type: nodes.SinkType(configMapString(sinkMap, "type")),
			Name: configMapString(sinkMap, "name"),
		}
		if c, ok := sinkMap["config"].(map[string]any); ok {
			target.Config = c
		}
		cfg.Sinks = append(cfg.Sinks, target)
	}

	return nodes.NewSinkNode(nd.ID, cfg), nil
}
