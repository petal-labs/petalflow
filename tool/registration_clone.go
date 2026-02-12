package tool

import (
	"slices"
	"strings"
)

func sortRegistrations(regs []ToolRegistration) {
	slices.SortFunc(regs, func(a, b ToolRegistration) int {
		return strings.Compare(a.Name, b.Name)
	})
}

func cloneRegistrations(in []ToolRegistration) []ToolRegistration {
	out := make([]ToolRegistration, len(in))
	for i := range in {
		out[i] = cloneRegistration(in[i])
	}
	return out
}

func cloneRegistration(in ToolRegistration) ToolRegistration {
	out := in
	if in.Config != nil {
		out.Config = make(map[string]string, len(in.Config))
		for k, v := range in.Config {
			out.Config[k] = v
		}
	}
	if in.Overlay != nil {
		overlay := *in.Overlay
		out.Overlay = &overlay
	}
	return out
}
