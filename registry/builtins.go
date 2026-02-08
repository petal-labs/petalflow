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
		Type:        "sink",
		Category:    "data",
		DisplayName: "Sink",
		Description: "Send data to external destinations (file, webhook, log, metric)",
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
}
