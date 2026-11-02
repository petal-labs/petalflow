package hydrate

import (
	"context"
	"fmt"
	"strings"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/tool"
)

// BuildActionToolRegistry constructs a core.ToolRegistry from persisted tool
// registrations. It registers action-level tool references as
// "<tool_name>.<action_name>" so graph nodes compiled from agent workflows can
// execute standalone tool actions.
// ToolRegistryOption customizes the action tool registry construction.
type ToolRegistryOption func(*tool.DaemonToolServiceConfig)

// WithAuditStore threads the audit recorder into the registry's
// invocation service so workflow-run invocations write the tenant-scoped
// lifecycle trail.
func WithAuditStore(store tool.ToolAuditStore) ToolRegistryOption {
	return func(cfg *tool.DaemonToolServiceConfig) {
		cfg.AuditStore = store
	}
}

// WithInvocationLimits attaches invocation budgets to the registry's
// invocation service.
func WithInvocationLimits(limits *tool.InvocationLimits) ToolRegistryOption {
	return func(cfg *tool.DaemonToolServiceConfig) {
		cfg.InvocationLimits = limits
	}
}

func BuildActionToolRegistry(ctx context.Context, store tool.Store, options ...ToolRegistryOption) (*core.ToolRegistry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		store = noopToolStore{}
	}

	serviceCfg := tool.DaemonToolServiceConfig{Store: store}
	for _, option := range options {
		option(&serviceCfg)
	}

	service, err := tool.NewDaemonToolService(serviceCfg)
	if err != nil {
		return nil, fmt.Errorf("creating tool service: %w", err)
	}

	registrations, err := service.List(ctx, tool.ToolListFilter{
		IncludeBuiltins: true,
	})
	if err != nil {
		return nil, fmt.Errorf("listing tool registrations: %w", err)
	}

	registry := core.NewToolRegistry()
	for _, registration := range registrations {
		if !registration.Enabled || registration.Status == tool.StatusDisabled {
			continue
		}

		for _, actionName := range registration.ActionNames() {
			reference := actionReference(registration.Name, actionName)
			if reference == "" {
				continue
			}
			registry.Register(serviceActionTool{
				name:       reference,
				toolName:   registration.Name,
				actionName: actionName,
				service:    service,
			})
		}
	}

	return registry, nil
}

type serviceActionTool struct {
	name       string
	toolName   string
	actionName string
	service    *tool.DaemonToolService
}

func (t serviceActionTool) Name() string {
	return t.name
}

func (t serviceActionTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	result, err := t.service.TestAction(ctx, t.toolName, t.actionName, args)
	if err != nil {
		return nil, err
	}
	if result.Outputs == nil {
		return map[string]any{}, nil
	}
	return result.Outputs, nil
}

func actionReference(toolName, actionName string) string {
	toolName = strings.TrimSpace(toolName)
	actionName = strings.TrimSpace(actionName)
	if toolName == "" || actionName == "" {
		return ""
	}
	return toolName + "." + actionName
}

type noopToolStore struct{}

func (noopToolStore) List(context.Context) ([]tool.ToolRegistration, error) {
	return nil, nil
}

func (noopToolStore) Get(context.Context, string) (tool.ToolRegistration, bool, error) {
	return tool.ToolRegistration{}, false, nil
}

func (noopToolStore) Upsert(context.Context, tool.ToolRegistration) error {
	return nil
}

func (noopToolStore) Delete(context.Context, string) error {
	return nil
}
