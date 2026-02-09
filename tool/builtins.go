package tool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"text/template"
)

var builtinNativeTools = map[string]NativeTool{
	"http_fetch":      httpFetchTool{},
	"template_render": templateRenderTool{},
}

// BuiltinRegistrations returns all built-in native tool registrations.
func BuiltinRegistrations() []ToolRegistration {
	names := make([]string, 0, len(builtinNativeTools))
	for name := range builtinNativeTools {
		names = append(names, name)
	}
	slices.Sort(names)

	regs := make([]ToolRegistration, 0, len(names))
	for _, name := range names {
		regs = append(regs, ToolRegistration{
			Name:     name,
			Origin:   OriginNative,
			Manifest: builtinNativeTools[name].Manifest(),
			Status:   StatusReady,
			Enabled:  true,
		})
	}
	return regs
}

// BuiltinRegistration returns a built-in registration by name.
func BuiltinRegistration(name string) (ToolRegistration, bool) {
	tool, ok := builtinNativeTools[name]
	if !ok {
		return ToolRegistration{}, false
	}

	return ToolRegistration{
		Name:     name,
		Origin:   OriginNative,
		Manifest: tool.Manifest(),
		Status:   StatusReady,
		Enabled:  true,
	}, true
}

// LookupBuiltinNativeTool returns a native built-in tool implementation by name.
func LookupBuiltinNativeTool(name string) (NativeTool, bool) {
	t, ok := builtinNativeTools[name]
	return t, ok
}

type templateRenderTool struct{}

func (templateRenderTool) Name() string {
	return "template_render"
}

func (templateRenderTool) Manifest() Manifest {
	manifest := NewManifest("template_render")
	manifest.Tool.Description = "Render a Go template string with provided variables."
	manifest.Tool.Version = "built-in"
	manifest.Transport = NewNativeTransport()
	manifest.Actions = map[string]ActionSpec{
		"render": {
			Description: "Render template content into a final string.",
			Inputs: map[string]FieldSpec{
				"template": {Type: TypeString, Required: true},
				"values": {
					Type:        TypeObject,
					Description: "Optional object of template values.",
				},
			},
			Outputs: map[string]FieldSpec{
				"rendered": {Type: TypeString},
			},
			Idempotent: true,
		},
	}
	return manifest
}

func (templateRenderTool) Invoke(ctx context.Context, action string, inputs map[string]any, config map[string]any) (map[string]any, error) {
	if action != "render" {
		return nil, fmt.Errorf("%w: %s", ErrActionNotFound, action)
	}

	templateBody, _ := inputs["template"].(string)
	if strings.TrimSpace(templateBody) == "" {
		return nil, fmt.Errorf("template_render: template input is required")
	}

	values := make(map[string]any)
	if explicitValues, ok := inputs["values"].(map[string]any); ok {
		for k, v := range explicitValues {
			values[k] = v
		}
	}
	for key, value := range inputs {
		if key == "template" || key == "values" {
			continue
		}
		values[key] = value
	}

	tpl, err := template.New("template_render").Option("missingkey=zero").Parse(templateBody)
	if err != nil {
		return nil, fmt.Errorf("template_render: parse template: %w", err)
	}

	var out bytes.Buffer
	if err := tpl.Execute(&out, values); err != nil {
		return nil, fmt.Errorf("template_render: execute template: %w", err)
	}

	return map[string]any{
		"rendered": out.String(),
	}, nil
}

type httpFetchTool struct{}

func (httpFetchTool) Name() string {
	return "http_fetch"
}

func (httpFetchTool) Manifest() Manifest {
	manifest := NewManifest("http_fetch")
	manifest.Tool.Description = "Fetch a URL over HTTP(S) and return status/body."
	manifest.Tool.Version = "built-in"
	manifest.Transport = NewNativeTransport()
	manifest.Actions = map[string]ActionSpec{
		"fetch": {
			Description: "Execute an HTTP request and return response data.",
			Inputs: map[string]FieldSpec{
				"url":    {Type: TypeString, Required: true},
				"method": {Type: TypeString},
				"body":   {Type: TypeString},
			},
			Outputs: map[string]FieldSpec{
				"status_code": {Type: TypeInteger},
				"body":        {Type: TypeString},
				"headers":     {Type: TypeObject},
			},
			Idempotent: false,
		},
	}
	manifest.Config = map[string]FieldSpec{
		"authorization": {
			Type:        TypeString,
			Sensitive:   true,
			Description: "Optional Authorization header value.",
		},
	}
	return manifest
}

func (httpFetchTool) Invoke(ctx context.Context, action string, inputs map[string]any, config map[string]any) (map[string]any, error) {
	if action != "fetch" {
		return nil, fmt.Errorf("%w: %s", ErrActionNotFound, action)
	}

	urlValue, _ := inputs["url"].(string)
	if strings.TrimSpace(urlValue) == "" {
		return nil, fmt.Errorf("http_fetch: url input is required")
	}

	method := http.MethodGet
	if rawMethod, ok := inputs["method"].(string); ok && strings.TrimSpace(rawMethod) != "" {
		method = strings.ToUpper(strings.TrimSpace(rawMethod))
	}

	var bodyReader io.Reader
	if body, ok := inputs["body"].(string); ok && body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlValue, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("http_fetch: build request: %w", err)
	}

	if auth, ok := config["authorization"].(string); ok && strings.TrimSpace(auth) != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http_fetch: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http_fetch: read response: %w", err)
	}

	headers := make(map[string]any, len(resp.Header))
	for key, values := range resp.Header {
		headers[key] = strings.Join(values, ", ")
	}

	return map[string]any{
		"status_code": resp.StatusCode,
		"body":        string(respBody),
		"headers":     headers,
	}, nil
}
