package registry

// registerBuiltins registers all built-in PetalFlow node types.
// Called once by Global() during singleton initialization.
func registerBuiltins(r *Registry) {
	r.Register(NodeTypeDef{
		Type:        "llm_prompt",
		Category:    "ai",
		DisplayName: "LLM Prompt",
		Description: "Send a prompt to a language model and receive a completion",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "string", Required: true},
				{Name: "context", Type: "string", Required: false},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "string"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "llm_router",
		Category:    "ai",
		DisplayName: "LLM Router",
		Description: "Use an LLM to classify input and route to a target node",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "string", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "string"},
				{Name: "decision", Type: "object"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "rule_router",
		Category:    "control",
		DisplayName: "Rule Router",
		Description: "Route to a target node based on conditional rules evaluated against envelope state",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
				{Name: "decision", Type: "object"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "filter",
		Category:    "data",
		DisplayName: "Filter",
		Description: "Filter collections (artifacts, messages, or variables) by rules",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "array", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "array"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "transform",
		Category:    "data",
		DisplayName: "Transform",
		Description: "Reshape data using pick, rename, flatten, template, or custom operations",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "merge",
		Category:    "control",
		DisplayName: "Merge",
		Description: "Combine multiple parallel branch outputs into a single envelope",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "tool",
		Category:    "tool",
		DisplayName: "Tool",
		Description: "Execute an external tool with arguments from the envelope",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "object", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "object"},
			},
		},
		IsTool:   true,
		ToolMode: "standalone",
	})

	r.Register(NodeTypeDef{
		Type:        "gate",
		Category:    "control",
		DisplayName: "Gate",
		Description: "Evaluate a condition and block, skip, or redirect execution",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "guardian",
		Category:    "control",
		DisplayName: "Guardian",
		Description: "Validate input data against a set of checks (type, pattern, PII, schema)",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
				{Name: "result", Type: "object"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "human",
		Category:    "control",
		DisplayName: "Human-in-the-Loop",
		Description: "Pause for human approval, choice, edit, or input",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
				{Name: "response", Type: "object"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "map",
		Category:    "control",
		DisplayName: "Map",
		Description: "Apply a node or function to each item in a collection",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "array", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "array"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "cache",
		Category:    "data",
		DisplayName: "Cache",
		Description: "Cache the output of a wrapped node to avoid repeated computation",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "webhook_trigger",
		Category:    "control",
		DisplayName: "Webhook Trigger",
		Description: "Receive inbound webhook payloads and map request context into workflow vars",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: false},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
				{Name: "request", Type: "object"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "webhook_call",
		Category:    "data",
		DisplayName: "Webhook Call",
		Description: "Send outbound HTTP webhook requests with envelope-derived payloads",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: false},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
				{Name: "response", Type: "object"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "noop",
		Category:    "control",
		DisplayName: "No-Op",
		Description: "Pass the envelope through unchanged (placeholder or testing)",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: false},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "func",
		Category:    "control",
		DisplayName: "Function",
		Description: "Execute a custom Go function as a node",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: false},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
			},
		},
	})

	r.Register(NodeTypeDef{
		Type:        "conditional",
		Category:    "control",
		DisplayName: "Conditional",
		Description: "Route data to different branches based on expression evaluation",
		Ports: PortSchema{
			Inputs: []PortDef{
				{Name: "input", Type: "any", Required: true},
			},
			Outputs: []PortDef{
				{Name: "output", Type: "any"},
				{Name: "decision", Type: "object"},
			},
		},
	})

	applyBuiltinConfigKeys(r)
}

// builtinConfigKeys lists the config keys the hydrate factory recognizes for
// each built-in node type. Keys not listed here trigger a non-fatal GR-020
// warning at load time. Types absent from this map (e.g. noop, func) accept no
// config and are not checked. Keep in sync with hydrate/llmfactory.go.
var builtinConfigKeys = map[string][]string{
	"llm_prompt":      {"provider", "model", "system_prompt", "prompt_template", "output_key", "temperature", "max_tokens"},
	"llm_router":      {"provider", "model", "system_prompt", "decision_key", "temperature", "allowed_targets"},
	"rule_router":     {"default_target", "decision_key", "allow_multiple", "rules"},
	"filter":          {"target", "input_var", "output_var", "stats_var", "filters"},
	"transform":       {"transform", "input_var", "output_var", "template", "format", "separator", "merge_strategy", "input_vars", "fields"},
	"merge":           {"output_key", "strategy", "var_name", "separator", "score_var", "higher_is_better"},
	"gate":            {"condition_var", "on_fail", "fail_message", "redirect_node_id", "result_var"},
	"guardian":        {"input_var", "on_fail", "fail_message", "redirect_node_id", "result_var", "stop_on_first_failure", "checks"},
	"human":           {"mode", "prompt", "output_var", "timeout"},
	"map":             {"input_var", "output_var", "item_var", "index_var", "concurrency", "continue_on_error", "preserve_order", "mapper_binding", "mapper_node"},
	"cache":           {"cache_key", "ttl", "output_var", "output_key", "input_vars", "include_artifacts", "include_input", "wrapped_binding", "wrapped_node"},
	"conditional":     {"default", "output_key", "evaluation_order", "pass_through", "conditions"},
	"webhook_trigger": {"methods", "auth", "request_var", "body_var", "headers_var", "query_var", "metadata_var", "timeout"},
	"webhook_call":    {"url", "method", "headers", "timeout", "max_response_bytes", "template", "result_var", "input_vars", "include_artifacts", "include_messages", "include_trace"},
	"tool":            {"tool_name", "args_template", "static_args", "output_key", "timeout", "required_args"},
}

// applyBuiltinConfigKeys attaches the recognized config keys to each registered
// built-in type, re-registering it (which overwrites in place, preserving order).
func applyBuiltinConfigKeys(r *Registry) {
	for typ, keys := range builtinConfigKeys {
		def, ok := r.Get(typ)
		if !ok {
			continue
		}
		def.ConfigKeys = keys
		r.Register(def)
	}
}
