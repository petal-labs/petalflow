package tool

import (
	"fmt"
)

// NativeLookup resolves a native tool implementation by registration name.
type NativeLookup func(name string) (NativeTool, bool)

// DefaultAdapterFactory resolves adapters using registration origin/transport.
type DefaultAdapterFactory struct {
	NativeLookup NativeLookup
}

// New builds a transport adapter for a tool registration.
func (f DefaultAdapterFactory) New(reg Registration) (Adapter, error) {
	switch reg.Origin {
	case OriginNative:
		if f.NativeLookup == nil {
			return nil, fmt.Errorf("tool: native lookup is not configured for %q", reg.Name)
		}
		nativeTool, ok := f.NativeLookup(reg.Name)
		if !ok {
			return nil, fmt.Errorf("tool: native tool %q is not registered", reg.Name)
		}
		return NewNativeAdapter(nativeTool), nil
	case OriginHTTP:
		return NewHTTPAdapter(reg), nil
	case OriginStdio:
		return NewStdioAdapter(reg), nil
	case OriginMCP:
		return nil, fmt.Errorf("tool: mcp adapter is not implemented yet")
	}

	// Fallback to transport type when origin was not persisted.
	switch reg.Manifest.Transport.Type {
	case TransportTypeNative:
		if f.NativeLookup == nil {
			return nil, fmt.Errorf("tool: native lookup is not configured for %q", reg.Name)
		}
		nativeTool, ok := f.NativeLookup(reg.Name)
		if !ok {
			return nil, fmt.Errorf("tool: native tool %q is not registered", reg.Name)
		}
		return NewNativeAdapter(nativeTool), nil
	case TransportTypeHTTP:
		return NewHTTPAdapter(reg), nil
	case TransportTypeStdio:
		return NewStdioAdapter(reg), nil
	case TransportTypeMCP:
		return nil, fmt.Errorf("tool: mcp adapter is not implemented yet")
	default:
		return nil, fmt.Errorf("tool: unsupported transport %q for %q", reg.Manifest.Transport.Type, reg.Name)
	}
}

var _ AdapterFactory = (*DefaultAdapterFactory)(nil)
