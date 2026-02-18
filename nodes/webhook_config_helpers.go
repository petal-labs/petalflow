package nodes

import "time"

func webhookConfigString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func webhookConfigMapString(m map[string]any, key string) string {
	return webhookConfigString(m, key)
}

func webhookConfigStringSlice(m map[string]any, key string) ([]string, bool) {
	raw, ok := m[key].([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			continue
		}
		out = append(out, s)
	}
	return out, true
}

func webhookConfigDuration(m map[string]any, key string) time.Duration {
	switch v := m[key].(type) {
	case string:
		d, _ := time.ParseDuration(v)
		return d
	case float64:
		return time.Duration(v * float64(time.Second))
	default:
		return 0
	}
}
