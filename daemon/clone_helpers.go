package daemon

import "github.com/petal-labs/petalflow/tool"

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneFieldMap(in map[string]tool.FieldSpec) map[string]tool.FieldSpec {
	if in == nil {
		return nil
	}
	out := make(map[string]tool.FieldSpec, len(in))
	for key, value := range in {
		copied := value
		if value.Items != nil {
			item := *value.Items
			copied.Items = &item
		}
		if value.Properties != nil {
			copied.Properties = cloneFieldMap(value.Properties)
		}
		out[key] = copied
	}
	return out
}
