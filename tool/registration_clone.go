package tool

func cloneRegistrations(in []ToolRegistration) []ToolRegistration {
	if in == nil {
		return nil
	}
	out := make([]ToolRegistration, 0, len(in))
	for _, reg := range in {
		out = append(out, cloneRegistration(reg))
	}
	return out
}

func cloneRegistration(in ToolRegistration) ToolRegistration {
	out := in
	out.Manifest = cloneManifest(in.Manifest)
	out.Config = cloneStringMap(in.Config)
	if in.Overlay != nil {
		overlay := *in.Overlay
		out.Overlay = &overlay
	}
	return out
}
