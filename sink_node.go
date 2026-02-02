package petalflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"text/template"
	"time"
)

// SinkType specifies the type of sink destination.
type SinkType string

const (
	// SinkTypeFile writes to a local file.
	SinkTypeFile SinkType = "file"

	// SinkTypeWebhook POSTs to an HTTP endpoint.
	SinkTypeWebhook SinkType = "webhook"

	// SinkTypeLog writes to structured logger.
	SinkTypeLog SinkType = "log"

	// SinkTypeMetric emits a metric.
	SinkTypeMetric SinkType = "metric"

	// SinkTypeVar stores in envelope var (useful for testing/composition).
	SinkTypeVar SinkType = "var"

	// SinkTypeCustom uses a custom sink function.
	SinkTypeCustom SinkType = "custom"
)

// SinkErrorPolicy determines behavior on sink failure.
type SinkErrorPolicy string

const (
	// SinkErrorPolicyFail fails the node on any sink error.
	SinkErrorPolicyFail SinkErrorPolicy = "fail"

	// SinkErrorPolicyContinue continues on sink errors.
	SinkErrorPolicyContinue SinkErrorPolicy = "continue"

	// SinkErrorPolicyRecord records errors and continues.
	SinkErrorPolicyRecord SinkErrorPolicy = "record"
)

// SinkTarget defines a single sink destination.
type SinkTarget struct {
	// Type specifies the sink type.
	Type SinkType

	// Name identifies this sink for error reporting.
	Name string

	// Config provides type-specific configuration.
	Config map[string]any

	// Condition optionally gates this sink.
	Condition func(ctx context.Context, env *Envelope) (bool, error)

	// CustomFunc is used when Type is SinkTypeCustom.
	CustomFunc func(ctx context.Context, data any, env *Envelope) error
}

// SinkResult contains the results of sink operations.
type SinkResult struct {
	Targets []SinkTargetResult `json:"targets"`
}

// SinkTargetResult contains the result of a single sink operation.
type SinkTargetResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

// SinkNodeConfig configures a SinkNode.
type SinkNodeConfig struct {
	// Sinks is a list of destinations to emit to.
	Sinks []SinkTarget

	// InputVars specifies which variables to include in output.
	// If empty, all vars are included.
	InputVars []string

	// IncludeArtifacts includes artifacts in the output.
	IncludeArtifacts bool

	// IncludeMessages includes messages in the output.
	IncludeMessages bool

	// IncludeTrace includes trace info in the output.
	IncludeTrace bool

	// Template renders a custom output format.
	// If empty, outputs structured data.
	Template string

	// ErrorPolicy determines behavior on sink failure.
	// Defaults to SinkErrorPolicyFail.
	ErrorPolicy SinkErrorPolicy

	// ResultVar stores sink results (success/failure per target).
	ResultVar string

	// HTTPClient is used for webhook sinks.
	// If nil, http.DefaultClient is used.
	HTTPClient HTTPClient

	// Logger is used for log sinks.
	// If nil, slog.Default() is used.
	Logger *slog.Logger

	// MetricRecorder is used for metric sinks.
	// If nil, metrics are logged instead.
	MetricRecorder MetricRecorder
}

// HTTPClient is an interface for making HTTP requests.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// MetricRecorder is an interface for recording metrics.
type MetricRecorder interface {
	// Record records a metric with the given name, value, and tags.
	Record(name string, value float64, tags map[string]string) error
}

// SinkNode pushes outputs to external systems.
type SinkNode struct {
	BaseNode
	config SinkNodeConfig
}

// NewSinkNode creates a new SinkNode with the given configuration.
func NewSinkNode(id string, config SinkNodeConfig) *SinkNode {
	// Set defaults
	if config.ErrorPolicy == "" {
		config.ErrorPolicy = SinkErrorPolicyFail
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	return &SinkNode{
		BaseNode: NewBaseNode(id, NodeKindSink),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *SinkNode) Config() SinkNodeConfig {
	return n.config
}

// Run executes the sink node logic.
func (n *SinkNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	// Check context
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Clone envelope first to ensure isolation
	resultEnv := env.Clone()

	// Build output data from original envelope
	outputData, err := n.buildOutputData(env)
	if err != nil {
		return nil, fmt.Errorf("sink node %s: failed to build output data: %w", n.id, err)
	}

	// Render template if configured
	var output any = outputData
	if n.config.Template != "" {
		rendered, err := n.renderTemplate(outputData)
		if err != nil {
			return nil, fmt.Errorf("sink node %s: template error: %w", n.id, err)
		}
		output = rendered
	}

	// Execute sinks (pass resultEnv for VarSink to modify)
	results := make([]SinkTargetResult, 0, len(n.config.Sinks))
	var firstError error

	for _, sink := range n.config.Sinks {
		result := n.executeSink(ctx, sink, output, resultEnv)
		results = append(results, result)

		if !result.Success && !result.Skipped && firstError == nil {
			firstError = fmt.Errorf("sink %q failed: %s", sink.Name, result.Error)
		}
	}

	// Store sink result if configured
	if n.config.ResultVar != "" {
		resultEnv.SetVar(n.config.ResultVar, SinkResult{Targets: results})
	}

	// Handle error based on policy
	if firstError != nil {
		switch n.config.ErrorPolicy {
		case SinkErrorPolicyFail:
			return nil, fmt.Errorf("sink node %s: %w", n.id, firstError)
		case SinkErrorPolicyRecord:
			resultEnv.AppendError(NodeError{
				NodeID:  n.id,
				Kind:    NodeKindSink,
				Message: firstError.Error(),
				At:      time.Now(),
			})
		case SinkErrorPolicyContinue:
			// Continue without recording
		}
	}

	return resultEnv, nil
}

// buildOutputData builds the data to send to sinks.
func (n *SinkNode) buildOutputData(env *Envelope) (map[string]any, error) {
	data := make(map[string]any)

	// Include vars
	if len(n.config.InputVars) > 0 {
		vars := make(map[string]any)
		for _, varName := range n.config.InputVars {
			if val, ok := env.GetVar(varName); ok {
				vars[varName] = val
			}
		}
		data["vars"] = vars
	} else if len(env.Vars) > 0 {
		// Include all vars
		vars := make(map[string]any)
		for k, v := range env.Vars {
			vars[k] = v
		}
		data["vars"] = vars
	}

	// Include artifacts if configured
	if n.config.IncludeArtifacts && len(env.Artifacts) > 0 {
		artifacts := make([]map[string]any, len(env.Artifacts))
		for i, art := range env.Artifacts {
			artifacts[i] = map[string]any{
				"id":        art.ID,
				"type":      art.Type,
				"mime_type": art.MimeType,
				"text":      art.Text,
				"uri":       art.URI,
				"meta":      art.Meta,
			}
		}
		data["artifacts"] = artifacts
	}

	// Include messages if configured
	if n.config.IncludeMessages && len(env.Messages) > 0 {
		messages := make([]map[string]any, len(env.Messages))
		for i, msg := range env.Messages {
			messages[i] = map[string]any{
				"role":    msg.Role,
				"content": msg.Content,
				"name":    msg.Name,
				"meta":    msg.Meta,
			}
		}
		data["messages"] = messages
	}

	// Include trace if configured
	if n.config.IncludeTrace {
		data["trace"] = map[string]any{
			"run_id":    env.Trace.RunID,
			"parent_id": env.Trace.ParentID,
			"span_id":   env.Trace.SpanID,
			"started":   env.Trace.Started,
		}
	}

	// Include input if present
	if env.Input != nil {
		data["input"] = env.Input
	}

	return data, nil
}

// renderTemplate renders the output template.
func (n *SinkNode) renderTemplate(data map[string]any) (string, error) {
	tmpl, err := template.New("sink").Funcs(sinkTemplateFuncs()).Parse(n.config.Template)
	if err != nil {
		return "", fmt.Errorf("invalid template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}

	return buf.String(), nil
}

// sinkTemplateFuncs returns template functions for sink templates.
func sinkTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"json": func(v any) string {
			b, err := json.Marshal(v)
			if err != nil {
				return fmt.Sprintf("error: %v", err)
			}
			return string(b)
		},
		"jsonPretty": func(v any) string {
			b, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return fmt.Sprintf("error: %v", err)
			}
			return string(b)
		},
	}
}

// executeSink executes a single sink target.
func (n *SinkNode) executeSink(ctx context.Context, sink SinkTarget, data any, env *Envelope) SinkTargetResult {
	result := SinkTargetResult{
		Name: sink.Name,
	}

	// Check condition if present
	if sink.Condition != nil {
		shouldRun, err := sink.Condition(ctx, env)
		if err != nil {
			result.Error = fmt.Sprintf("condition error: %v", err)
			return result
		}
		if !shouldRun {
			result.Success = true
			result.Skipped = true
			return result
		}
	}

	// Execute based on sink type
	var err error
	switch sink.Type {
	case SinkTypeFile:
		err = n.executeFileSink(ctx, sink, data)
	case SinkTypeWebhook:
		err = n.executeWebhookSink(ctx, sink, data)
	case SinkTypeLog:
		err = n.executeLogSink(ctx, sink, data)
	case SinkTypeMetric:
		err = n.executeMetricSink(ctx, sink, data, env)
	case SinkTypeVar:
		err = n.executeVarSink(ctx, sink, data, env)
	case SinkTypeCustom:
		if sink.CustomFunc != nil {
			err = sink.CustomFunc(ctx, data, env)
		} else {
			err = fmt.Errorf("custom sink requires CustomFunc")
		}
	default:
		err = fmt.Errorf("unknown sink type: %s", sink.Type)
	}

	if err != nil {
		result.Error = err.Error()
	} else {
		result.Success = true
	}

	return result
}

// executeFileSink writes data to a file.
func (n *SinkNode) executeFileSink(ctx context.Context, sink SinkTarget, data any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	path, _ := sink.Config["path"].(string)
	if path == "" {
		return fmt.Errorf("file sink requires 'path' config")
	}

	format, _ := sink.Config["format"].(string)
	if format == "" {
		format = "json"
	}

	mode, _ := sink.Config["mode"].(string)
	if mode == "" {
		mode = "overwrite"
	}

	// Convert data to bytes based on format
	var content []byte
	var err error

	switch format {
	case "json":
		content, err = json.MarshalIndent(data, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		content = append(content, '\n')
	case "text":
		switch v := data.(type) {
		case string:
			content = []byte(v)
		case []byte:
			content = v
		default:
			content = []byte(fmt.Sprintf("%v", data))
		}
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write file based on mode
	var flag int
	switch mode {
	case "append":
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	case "overwrite":
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	default:
		return fmt.Errorf("unsupported mode: %s", mode)
	}

	f, err := os.OpenFile(path, flag, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// executeWebhookSink POSTs data to an HTTP endpoint.
func (n *SinkNode) executeWebhookSink(ctx context.Context, sink SinkTarget, data any) error {
	url, _ := sink.Config["url"].(string)
	if url == "" {
		return fmt.Errorf("webhook sink requires 'url' config")
	}

	method, _ := sink.Config["method"].(string)
	if method == "" {
		method = "POST"
	}

	timeout, _ := sink.Config["timeout"].(string)
	if timeout != "" {
		d, err := time.ParseDuration(timeout)
		if err == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}

	// Marshal data
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Add custom headers
	if headers, ok := sink.Config["headers"].(map[string]any); ok {
		for k, v := range headers {
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}

	// Execute request
	resp, err := n.config.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Drain body to allow connection reuse
	_, _ = io.Copy(io.Discard, resp.Body)

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// executeLogSink writes to structured logger.
func (n *SinkNode) executeLogSink(ctx context.Context, sink SinkTarget, data any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	level, _ := sink.Config["level"].(string)
	if level == "" {
		level = "info"
	}

	message, _ := sink.Config["message"].(string)
	if message == "" {
		message = "sink output"
	}

	// Build log attributes
	attrs := make([]any, 0)

	// Add specific fields if configured
	if fields, ok := sink.Config["fields"].([]any); ok {
		if dataMap, ok := data.(map[string]any); ok {
			for _, field := range fields {
				if fieldName, ok := field.(string); ok {
					if val, exists := getNestedMapValue(dataMap, fieldName); exists {
						attrs = append(attrs, slog.Any(fieldName, val))
					}
				}
			}
		}
	} else {
		// Add all data as attributes
		attrs = append(attrs, slog.Any("data", data))
	}

	// Log at appropriate level
	switch level {
	case "debug":
		n.config.Logger.DebugContext(ctx, message, attrs...)
	case "info":
		n.config.Logger.InfoContext(ctx, message, attrs...)
	case "warn", "warning":
		n.config.Logger.WarnContext(ctx, message, attrs...)
	case "error":
		n.config.Logger.ErrorContext(ctx, message, attrs...)
	default:
		n.config.Logger.InfoContext(ctx, message, attrs...)
	}

	return nil
}

// executeMetricSink emits a metric.
func (n *SinkNode) executeMetricSink(ctx context.Context, sink SinkTarget, data any, env *Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	name, _ := sink.Config["name"].(string)
	if name == "" {
		return fmt.Errorf("metric sink requires 'name' config")
	}

	// Get value from data or config
	value := 1.0 // Default to 1 for counter-style metrics

	if valueField, ok := sink.Config["value_field"].(string); ok {
		if dataMap, ok := data.(map[string]any); ok {
			if val, exists := getNestedMapValue(dataMap, valueField); exists {
				if floatVal, ok := toFloat64(val); ok {
					value = floatVal
				}
			}
		}
	} else if configValue, ok := sink.Config["value"].(float64); ok {
		value = configValue
	} else if configValue, ok := sink.Config["value"].(int); ok {
		value = float64(configValue)
	}

	// Build tags
	tags := make(map[string]string)
	if configTags, ok := sink.Config["tags"].(map[string]any); ok {
		for k, v := range configTags {
			tags[k] = fmt.Sprintf("%v", v)
		}
	}

	// Record metric
	if n.config.MetricRecorder != nil {
		return n.config.MetricRecorder.Record(name, value, tags)
	}

	// Fallback: log the metric
	n.config.Logger.InfoContext(ctx, "metric",
		slog.String("name", name),
		slog.Float64("value", value),
		slog.Any("tags", tags),
	)

	return nil
}

// executeVarSink stores data in an envelope variable.
func (n *SinkNode) executeVarSink(ctx context.Context, sink SinkTarget, data any, env *Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	varName, _ := sink.Config["name"].(string)
	if varName == "" {
		return fmt.Errorf("var sink requires 'name' config")
	}

	env.SetVar(varName, data)
	return nil
}

// getNestedMapValue retrieves a value from nested maps using dot notation.
func getNestedMapValue(data map[string]any, path string) (any, bool) {
	parts := splitPath(path)
	current := any(data)

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = v[part]
			if !ok {
				return nil, false
			}
		default:
			return nil, false
		}
	}

	return current, true
}

// splitPath splits a dot-notation path into parts.
func splitPath(path string) []string {
	var parts []string
	var current string

	for _, c := range path {
		if c == '.' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}

	if current != "" {
		parts = append(parts, current)
	}

	return parts
}

// MockHTTPClient is a mock HTTP client for testing.
type MockHTTPClient struct {
	Requests   []*http.Request
	Response   *http.Response
	Error      error
	StatusCode int
}

// NewMockHTTPClient creates a new mock HTTP client.
func NewMockHTTPClient(statusCode int) *MockHTTPClient {
	return &MockHTTPClient{
		StatusCode: statusCode,
		Requests:   make([]*http.Request, 0),
	}
}

// Do implements HTTPClient interface.
func (c *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.Requests = append(c.Requests, req)

	if c.Error != nil {
		return nil, c.Error
	}

	if c.Response != nil {
		return c.Response, nil
	}

	return &http.Response{
		StatusCode: c.StatusCode,
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}, nil
}

// MockMetricRecorder is a mock metric recorder for testing.
type MockMetricRecorder struct {
	Metrics []MockMetric
}

// MockMetric represents a recorded metric.
type MockMetric struct {
	Name  string
	Value float64
	Tags  map[string]string
}

// NewMockMetricRecorder creates a new mock metric recorder.
func NewMockMetricRecorder() *MockMetricRecorder {
	return &MockMetricRecorder{
		Metrics: make([]MockMetric, 0),
	}
}

// Record implements MetricRecorder interface.
func (r *MockMetricRecorder) Record(name string, value float64, tags map[string]string) error {
	r.Metrics = append(r.Metrics, MockMetric{
		Name:  name,
		Value: value,
		Tags:  tags,
	})
	return nil
}

// Ensure interface compliance at compile time.
var _ Node = (*SinkNode)(nil)
var _ HTTPClient = (*MockHTTPClient)(nil)
var _ MetricRecorder = (*MockMetricRecorder)(nil)
