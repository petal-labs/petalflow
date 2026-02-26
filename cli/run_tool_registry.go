package cli

import (
	"context"
	"sync"

	"github.com/petal-labs/petalflow/daemon"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/tool"
)

var (
	runDynamicTypeMu sync.Mutex
	runDynamicTypes  = map[string]struct{}{}
)

func syncRunToolNodeTypes(ctx context.Context, store tool.Store) error {
	service, err := tool.NewDaemonToolService(tool.DaemonToolServiceConfig{
		Store: store,
	})
	if err != nil {
		return err
	}

	regs, err := service.List(ctx, tool.ToolListFilter{
		IncludeBuiltins: false,
	})
	if err != nil {
		return err
	}

	nodeDefs := daemon.BuildToolNodeTypes(regs)
	reg := registry.Global()

	runDynamicTypeMu.Lock()
	defer runDynamicTypeMu.Unlock()

	for typeName := range runDynamicTypes {
		reg.Delete(typeName)
	}

	next := make(map[string]struct{}, len(nodeDefs))
	for _, def := range nodeDefs {
		reg.Register(def)
		next[def.Type] = struct{}{}
	}
	runDynamicTypes = next

	return nil
}

type runNoopToolStore struct{}

func (runNoopToolStore) List(context.Context) ([]tool.ToolRegistration, error) {
	return nil, nil
}

func (runNoopToolStore) Get(context.Context, string) (tool.ToolRegistration, bool, error) {
	return tool.ToolRegistration{}, false, nil
}

func (runNoopToolStore) Upsert(context.Context, tool.ToolRegistration) error {
	return nil
}

func (runNoopToolStore) Delete(context.Context, string) error {
	return nil
}
