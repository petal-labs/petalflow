// Package petalflow provides a Go-native framework for building, orchestrating,
// and deploying AI agents and multimodal workflows.
//
// This file provides backward-compatible re-exports for all types and constructors
// from the core, graph, runtime, and nodes subpackages. Existing code using
// petalflow.* imports will continue to work without modification.
//
// For new code, consider importing subpackages directly for clearer dependencies:
//
//	import "github.com/petal-labs/petalflow/core"
//	import "github.com/petal-labs/petalflow/graph"
//	import "github.com/petal-labs/petalflow/runtime"
//	import "github.com/petal-labs/petalflow/nodes"
package petalflow

import (
	"context"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
)

// =============================================================================
// Core Package Re-exports
// =============================================================================

// Type aliases from core package
type (
	// NodeKind identifies the type of a node.
	NodeKind = core.NodeKind

	// Message is a chat-style message used for LLM steps and auditing.
	Message = core.Message

	// Artifact represents a document or derived data produced during a run.
	Artifact = core.Artifact

	// TraceInfo is propagated by the runtime for observability and replay.
	TraceInfo = core.TraceInfo

	// NodeError is recorded when nodes fail but the graph continues.
	NodeError = core.NodeError

	// RouteDecision is produced by RouterNode to indicate which targets to activate.
	RouteDecision = core.RouteDecision

	// RetryPolicy configures retry behavior for nodes that call external systems.
	RetryPolicy = core.RetryPolicy

	// Budget is an optional guardrail for LLM calls to limit resource usage.
	Budget = core.Budget

	// TokenUsage tracks token consumption for cost tracking and budgeting.
	TokenUsage = core.TokenUsage

	// ErrorPolicy defines how a node handles errors.
	ErrorPolicy = core.ErrorPolicy

	// LLMClient abstracts a single provider/model backend for PetalFlow.
	LLMClient = core.LLMClient

	// StreamingLLMClient extends LLMClient with streaming capability.
	StreamingLLMClient = core.StreamingLLMClient

	// StreamChunk is a partial response from the LLM.
	StreamChunk = core.StreamChunk

	// LLMRequest is the request structure for LLM completion.
	LLMRequest = core.LLMRequest

	// LLMResponse captures the output from an LLM call.
	LLMResponse = core.LLMResponse

	// LLMMessage is a chat message in PetalFlow format.
	LLMMessage = core.LLMMessage

	// LLMTokenUsage tracks token consumption for LLM calls.
	LLMTokenUsage = core.LLMTokenUsage

	// LLMToolCall represents a tool invocation requested by the model.
	LLMToolCall = core.LLMToolCall

	// LLMToolResult represents the result of executing a tool.
	LLMToolResult = core.LLMToolResult

	// LLMReasoningOutput contains reasoning information from the model.
	LLMReasoningOutput = core.LLMReasoningOutput

	// PetalTool is the tool interface for PetalFlow.
	PetalTool = core.PetalTool

	// FuncTool is a simple function-backed tool for PetalFlow.
	FuncTool = core.FuncTool

	// ToolRegistry holds a collection of tools for lookup by name.
	ToolRegistry = core.ToolRegistry

	// Node is the fundamental unit of execution in a PetalFlow graph.
	Node = core.Node

	// MergeCapable is implemented by nodes that can merge multiple input envelopes.
	MergeCapable = core.MergeCapable

	// RouterNode is a node that can select which edges to activate.
	RouterNode = core.RouterNode

	// BaseNode provides common functionality for node implementations.
	BaseNode = core.BaseNode

	// NoopNode is a node that passes the envelope through unchanged.
	NoopNode = core.NoopNode

	// FuncNode wraps a function as a Node.
	FuncNode = core.FuncNode

	// NodeFunc is a convenience type for node functions.
	NodeFunc = core.NodeFunc

	// Envelope is the single data structure passed between nodes.
	Envelope = core.Envelope
)

// NodeKind constants
const (
	NodeKindLLM       = core.NodeKindLLM
	NodeKindTool      = core.NodeKindTool
	NodeKindRouter    = core.NodeKindRouter
	NodeKindMerge     = core.NodeKindMerge
	NodeKindMap       = core.NodeKindMap
	NodeKindGate      = core.NodeKindGate
	NodeKindNoop      = core.NodeKindNoop
	NodeKindFilter    = core.NodeKindFilter
	NodeKindTransform = core.NodeKindTransform
	NodeKindGuardian  = core.NodeKindGuardian
	NodeKindCache     = core.NodeKindCache
	NodeKindSink      = core.NodeKindSink
	NodeKindHuman     = core.NodeKindHuman
)

// ErrorPolicy constants
const (
	ErrorPolicyFail     = core.ErrorPolicyFail
	ErrorPolicyContinue = core.ErrorPolicyContinue
	ErrorPolicyRecord   = core.ErrorPolicyRecord
)

// Core package constructors
var (
	NewEnvelope        = core.NewEnvelope
	NewBaseNode        = core.NewBaseNode
	NewNoopNode        = core.NewNoopNode
	NewFuncNode        = core.NewFuncNode
	NewFuncTool        = core.NewFuncTool
	NewToolRegistry    = core.NewToolRegistry
	DefaultRetryPolicy = core.DefaultRetryPolicy
)

// =============================================================================
// Graph Package Re-exports
// =============================================================================

// Type aliases from graph package
type (
	// Graph represents a directed graph of nodes connected by edges.
	Graph = graph.Graph

	// Edge represents a directed connection between two nodes.
	Edge = graph.Edge

	// BasicGraph is a simple implementation of the Graph interface.
	BasicGraph = graph.BasicGraph

	// GraphBuilder provides a fluent API for constructing workflow graphs.
	GraphBuilder = graph.GraphBuilder

	// FanOutBranch is a helper for building complex fan-out patterns.
	FanOutBranch = graph.FanOutBranch
)

// Graph package errors
var (
	ErrNodeNotFound     = graph.ErrNodeNotFound
	ErrDuplicateNode    = graph.ErrDuplicateNode
	ErrInvalidEdge      = graph.ErrInvalidEdge
	ErrNoEntryNode      = graph.ErrNoEntryNode
	ErrCycleDetected    = graph.ErrCycleDetected
	ErrEmptyGraph       = graph.ErrEmptyGraph
	ErrNodeAlreadyAdded = graph.ErrNodeAlreadyAdded
)

// Graph package constructors
var (
	NewGraph          = graph.NewGraph
	NewGraphBuilder   = graph.NewGraphBuilder
	NewBranch         = graph.NewBranch
	NewPipelineBranch = graph.NewPipelineBranch
)

// =============================================================================
// Runtime Package Re-exports
// =============================================================================

// Type aliases from runtime package
type (
	// Runtime executes graphs and emits events.
	Runtime = runtime.Runtime

	// RunOptions controls execution behavior.
	RunOptions = runtime.RunOptions

	// BasicRuntime is a simple sequential runtime implementation.
	BasicRuntime = runtime.BasicRuntime

	// EventKind identifies the type of event emitted by the runtime.
	EventKind = runtime.EventKind

	// Event is a structured, streamable record of what happened during execution.
	Event = runtime.Event

	// EventEmitter is a function type for emitting events.
	EventEmitter = runtime.EventEmitter

	// EventHandler is a function type for handling events.
	EventHandler = runtime.EventHandler

	// StepController is the interface for controlling step-through execution.
	StepController = runtime.StepController

	// StepAction specifies what the runtime should do at a step point.
	StepAction = runtime.StepAction

	// StepPoint indicates when a step pause occurred.
	StepPoint = runtime.StepPoint

	// StepRequest contains information about the current step point.
	StepRequest = runtime.StepRequest

	// StepResponse contains the controller's decision.
	StepResponse = runtime.StepResponse

	// EnvelopeSnapshot is a read-only view of the envelope state.
	EnvelopeSnapshot = runtime.EnvelopeSnapshot

	// EnvelopeModification specifies changes to apply to the envelope.
	EnvelopeModification = runtime.EnvelopeModification

	// GraphSnapshot provides read-only graph context.
	GraphSnapshot = runtime.GraphSnapshot

	// StepConfig configures step-through behavior.
	StepConfig = runtime.StepConfig

	// StepCallback is the function signature for CallbackStepController.
	StepCallback = runtime.StepCallback

	// ShouldPauseFunc is an optional predicate for CallbackStepController.
	ShouldPauseFunc = runtime.ShouldPauseFunc

	// CallbackStepController invokes a function at each step.
	CallbackStepController = runtime.CallbackStepController

	// ChannelStepController uses Go channels for interactive debugging.
	ChannelStepController = runtime.ChannelStepController

	// BreakpointStepController only pauses at specified breakpoints.
	BreakpointStepController = runtime.BreakpointStepController

	// AutoStepController automatically continues with a configurable delay.
	AutoStepController = runtime.AutoStepController

	// StepHandler is called when a breakpoint is hit.
	StepHandler = runtime.StepHandler
)

// EventKind constants
const (
	EventRunStarted    = runtime.EventRunStarted
	EventNodeStarted   = runtime.EventNodeStarted
	EventNodeOutput    = runtime.EventNodeOutput
	EventNodeFailed    = runtime.EventNodeFailed
	EventNodeFinished  = runtime.EventNodeFinished
	EventRouteDecision = runtime.EventRouteDecision
	EventRunFinished   = runtime.EventRunFinished
	EventStepPaused    = runtime.EventStepPaused
	EventStepResumed   = runtime.EventStepResumed
	EventStepSkipped   = runtime.EventStepSkipped
	EventStepAborted   = runtime.EventStepAborted
)

// StepAction constants
const (
	StepActionContinue        = runtime.StepActionContinue
	StepActionSkipNode        = runtime.StepActionSkipNode
	StepActionAbort           = runtime.StepActionAbort
	StepActionRunToBreakpoint = runtime.StepActionRunToBreakpoint
)

// StepPoint constants
const (
	StepPointBeforeNode = runtime.StepPointBeforeNode
	StepPointAfterNode  = runtime.StepPointAfterNode
)

// Runtime package errors
var (
	ErrMaxHopsExceeded     = runtime.ErrMaxHopsExceeded
	ErrRunCanceled         = runtime.ErrRunCanceled
	ErrNodeExecution       = runtime.ErrNodeExecution
	ErrStepAborted         = runtime.ErrStepAborted
	ErrStepRequestNotFound = runtime.ErrStepRequestNotFound
)

// Runtime package constructors
var (
	NewRuntime                  = runtime.NewRuntime
	DefaultRunOptions           = runtime.DefaultRunOptions
	NewEvent                    = runtime.NewEvent
	MultiEventHandler           = runtime.MultiEventHandler
	ChannelEventHandler         = runtime.ChannelEventHandler
	DefaultStepConfig           = runtime.DefaultStepConfig
	NewCallbackStepController   = runtime.NewCallbackStepController
	NewChannelStepController    = runtime.NewChannelStepController
	NewBreakpointStepController = runtime.NewBreakpointStepController
	NewAutoStepController       = runtime.NewAutoStepController
)

// =============================================================================
// Nodes Package Re-exports
// =============================================================================

// Type aliases from nodes package
type (
	// LLMNode calls a language model to process the envelope.
	LLMNode = nodes.LLMNode

	// LLMNodeConfig configures an LLMNode.
	LLMNodeConfig = nodes.LLMNodeConfig

	// ToolNode executes a tool and stores the result.
	ToolNode = nodes.ToolNode

	// ToolNodeConfig configures a ToolNode.
	ToolNodeConfig = nodes.ToolNodeConfig

	// RuleRouter routes based on configured rules.
	RuleRouter = nodes.RuleRouter

	// RuleRouterConfig configures a RuleRouter.
	RuleRouterConfig = nodes.RuleRouterConfig

	// RouteRule defines a single routing rule.
	RouteRule = nodes.RouteRule

	// RouteCondition defines a condition for routing.
	RouteCondition = nodes.RouteCondition

	// ConditionOp is the operator for a route condition.
	ConditionOp = nodes.ConditionOp

	// LLMRouter uses an LLM to make routing decisions.
	LLMRouter = nodes.LLMRouter

	// LLMRouterConfig configures an LLMRouter.
	LLMRouterConfig = nodes.LLMRouterConfig

	// MergeNode combines multiple input envelopes into one.
	MergeNode = nodes.MergeNode

	// MergeNodeConfig configures a MergeNode.
	MergeNodeConfig = nodes.MergeNodeConfig

	// MergeStrategy defines how to combine multiple envelopes.
	MergeStrategy = nodes.MergeStrategy

	// JSONMergeStrategy merges envelopes by combining JSON data.
	JSONMergeStrategy = nodes.JSONMergeStrategy

	// JSONMergeConfig configures a JSONMergeStrategy.
	JSONMergeConfig = nodes.JSONMergeConfig

	// ConcatMergeStrategy merges by concatenating values.
	ConcatMergeStrategy = nodes.ConcatMergeStrategy

	// ConcatMergeConfig configures a ConcatMergeStrategy.
	ConcatMergeConfig = nodes.ConcatMergeConfig

	// BestScoreMergeStrategy selects the envelope with the best score.
	BestScoreMergeStrategy = nodes.BestScoreMergeStrategy

	// BestScoreMergeConfig configures a BestScoreMergeStrategy.
	BestScoreMergeConfig = nodes.BestScoreMergeConfig

	// FuncMergeStrategy uses a custom function for merging.
	FuncMergeStrategy = nodes.FuncMergeStrategy

	// AllMergeStrategy collects all inputs into a single output.
	AllMergeStrategy = nodes.AllMergeStrategy

	// MapNode applies a sub-node to each item in a collection.
	MapNode = nodes.MapNode

	// MapNodeConfig configures a MapNode.
	MapNodeConfig = nodes.MapNodeConfig

	// FilterNode filters items based on conditions.
	FilterNode = nodes.FilterNode

	// FilterNodeConfig configures a FilterNode.
	FilterNodeConfig = nodes.FilterNodeConfig

	// FilterOp defines a filter operation.
	FilterOp = nodes.FilterOp

	// FilterOpType is the type of filter operation.
	FilterOpType = nodes.FilterOpType

	// FilterTarget specifies what to filter.
	FilterTarget = nodes.FilterTarget

	// FilterStats tracks filter operation statistics.
	FilterStats = nodes.FilterStats

	// TransformNode reshapes or transforms data.
	TransformNode = nodes.TransformNode

	// TransformNodeConfig configures a TransformNode.
	TransformNodeConfig = nodes.TransformNodeConfig

	// TransformType specifies the type of transformation.
	TransformType = nodes.TransformType

	// GateNode evaluates a condition and either passes execution or takes action.
	GateNode = nodes.GateNode

	// GateNodeConfig configures a GateNode.
	GateNodeConfig = nodes.GateNodeConfig

	// GateAction defines what happens when a gate condition fails.
	GateAction = nodes.GateAction

	// GateResult is stored in the envelope when ResultVar is set.
	GateResult = nodes.GateResult

	// CacheNode wraps another node and caches its results.
	CacheNode = nodes.CacheNode

	// CacheNodeConfig configures a CacheNode.
	CacheNodeConfig = nodes.CacheNodeConfig

	// CacheStore is the interface for cache storage backends.
	CacheStore = nodes.CacheStore

	// CacheResult contains metadata about a cache operation.
	CacheResult = nodes.CacheResult

	// MemoryCacheStore is an in-memory cache implementation.
	MemoryCacheStore = nodes.MemoryCacheStore

	// CacheKeyBuilder helps build complex cache keys.
	CacheKeyBuilder = nodes.CacheKeyBuilder

	// MockNode is a simple node for testing.
	MockNode = nodes.MockNode

	// GuardianNode validates data against defined checks.
	GuardianNode = nodes.GuardianNode

	// GuardianNodeConfig configures a GuardianNode.
	GuardianNodeConfig = nodes.GuardianNodeConfig

	// GuardianCheck defines a validation check.
	GuardianCheck = nodes.GuardianCheck

	// GuardianCheckType is the type of check to perform.
	GuardianCheckType = nodes.GuardianCheckType

	// GuardianAction defines what happens when a check fails.
	GuardianAction = nodes.GuardianAction

	// GuardianFailure records a single check failure.
	GuardianFailure = nodes.GuardianFailure

	// GuardianResult contains the results of all checks.
	GuardianResult = nodes.GuardianResult

	// PIIType identifies the type of PII detected.
	PIIType = nodes.PIIType

	// HumanNode requests human input or approval.
	HumanNode = nodes.HumanNode

	// HumanNodeConfig configures a HumanNode.
	HumanNodeConfig = nodes.HumanNodeConfig

	// HumanHandler processes human requests.
	HumanHandler = nodes.HumanHandler

	// HumanRequest represents a request for human input.
	HumanRequest = nodes.HumanRequest

	// HumanResponse contains the human's response.
	HumanResponse = nodes.HumanResponse

	// HumanRequestType identifies the type of human request.
	HumanRequestType = nodes.HumanRequestType

	// HumanTimeoutAction defines what happens on timeout.
	HumanTimeoutAction = nodes.HumanTimeoutAction

	// HumanOption represents a choice option.
	HumanOption = nodes.HumanOption

	// ChannelHumanHandler uses Go channels for human interaction.
	ChannelHumanHandler = nodes.ChannelHumanHandler

	// CallbackHumanHandler uses a callback function.
	CallbackHumanHandler = nodes.CallbackHumanHandler

	// AutoApproveHandler automatically approves or rejects requests.
	AutoApproveHandler = nodes.AutoApproveHandler

	// QueuedHumanHandler queues requests for later processing.
	QueuedHumanHandler = nodes.QueuedHumanHandler

	// SinkNode outputs data to external systems.
	SinkNode = nodes.SinkNode

	// SinkNodeConfig configures a SinkNode.
	SinkNodeConfig = nodes.SinkNodeConfig

	// SinkType identifies the type of sink.
	SinkType = nodes.SinkType

	// SinkErrorPolicy defines how sink errors are handled.
	SinkErrorPolicy = nodes.SinkErrorPolicy

	// SinkTarget defines a single sink destination.
	SinkTarget = nodes.SinkTarget

	// SinkResult contains the results of sink operations.
	SinkResult = nodes.SinkResult

	// SinkTargetResult contains the result of a single sink target.
	SinkTargetResult = nodes.SinkTargetResult

	// HTTPClient is the interface for HTTP requests.
	HTTPClient = nodes.HTTPClient

	// MetricRecorder is the interface for recording metrics.
	MetricRecorder = nodes.MetricRecorder

	// MockHTTPClient is a mock HTTP client for testing.
	MockHTTPClient = nodes.MockHTTPClient

	// MockMetricRecorder is a mock metric recorder for testing.
	MockMetricRecorder = nodes.MockMetricRecorder

	// MockMetric represents a recorded metric.
	MockMetric = nodes.MockMetric
)

// ConditionOp constants
const (
	OpEquals      = nodes.OpEquals
	OpNotEquals   = nodes.OpNotEquals
	OpContains    = nodes.OpContains
	OpGreaterThan = nodes.OpGreaterThan
	OpLessThan    = nodes.OpLessThan
	OpExists      = nodes.OpExists
	OpNotExists   = nodes.OpNotExists
	OpIn          = nodes.OpIn
)

// GateAction constants
const (
	GateActionBlock    = nodes.GateActionBlock
	GateActionSkip     = nodes.GateActionSkip
	GateActionRedirect = nodes.GateActionRedirect
)

// FilterOpType constants
const (
	FilterOpTopN      = nodes.FilterOpTopN
	FilterOpThreshold = nodes.FilterOpThreshold
	FilterOpDedupe    = nodes.FilterOpDedupe
	FilterOpByType    = nodes.FilterOpByType
	FilterOpMatch     = nodes.FilterOpMatch
	FilterOpExclude   = nodes.FilterOpExclude
	FilterOpCustom    = nodes.FilterOpCustom
)

// FilterTarget constants
const (
	FilterTargetArtifacts = nodes.FilterTargetArtifacts
	FilterTargetMessages  = nodes.FilterTargetMessages
	FilterTargetVar       = nodes.FilterTargetVar
)

// TransformType constants
const (
	TransformPick      = nodes.TransformPick
	TransformOmit      = nodes.TransformOmit
	TransformRename    = nodes.TransformRename
	TransformFlatten   = nodes.TransformFlatten
	TransformMerge     = nodes.TransformMerge
	TransformTemplate  = nodes.TransformTemplate
	TransformStringify = nodes.TransformStringify
	TransformParse     = nodes.TransformParse
	TransformMap       = nodes.TransformMap
	TransformCustom    = nodes.TransformCustom
)

// GuardianCheckType constants
const (
	GuardianCheckRequired  = nodes.GuardianCheckRequired
	GuardianCheckMaxLength = nodes.GuardianCheckMaxLength
	GuardianCheckMinLength = nodes.GuardianCheckMinLength
	GuardianCheckPattern   = nodes.GuardianCheckPattern
	GuardianCheckEnum      = nodes.GuardianCheckEnum
	GuardianCheckTypeCheck = nodes.GuardianCheckType_
	GuardianCheckRange     = nodes.GuardianCheckRange
	GuardianCheckPII       = nodes.GuardianCheckPII
	GuardianCheckSchema    = nodes.GuardianCheckSchema
	GuardianCheckCustom    = nodes.GuardianCheckCustom
)

// GuardianAction constants
const (
	GuardianActionFail     = nodes.GuardianActionFail
	GuardianActionSkip     = nodes.GuardianActionSkip
	GuardianActionRedirect = nodes.GuardianActionRedirect
)

// PIIType constants
const (
	PIITypeSSN         = nodes.PIITypeSSN
	PIITypeEmail       = nodes.PIITypeEmail
	PIITypePhone       = nodes.PIITypePhone
	PIITypeCreditCard  = nodes.PIITypeCreditCard
	PIITypeIPAddress   = nodes.PIITypeIPAddress
	PIITypeDateOfBirth = nodes.PIITypeDateOfBirth
)

// HumanRequestType constants
const (
	HumanRequestApproval = nodes.HumanRequestApproval
	HumanRequestChoice   = nodes.HumanRequestChoice
	HumanRequestEdit     = nodes.HumanRequestEdit
	HumanRequestInput    = nodes.HumanRequestInput
	HumanRequestReview   = nodes.HumanRequestReview
)

// HumanTimeoutAction constants
const (
	HumanTimeoutFail    = nodes.HumanTimeoutFail
	HumanTimeoutDefault = nodes.HumanTimeoutDefault
	HumanTimeoutSkip    = nodes.HumanTimeoutSkip
)

// SinkType constants
const (
	SinkTypeFile    = nodes.SinkTypeFile
	SinkTypeWebhook = nodes.SinkTypeWebhook
	SinkTypeLog     = nodes.SinkTypeLog
	SinkTypeMetric  = nodes.SinkTypeMetric
	SinkTypeVar     = nodes.SinkTypeVar
	SinkTypeCustom  = nodes.SinkTypeCustom
)

// SinkErrorPolicy constants
const (
	SinkErrorPolicyFail     = nodes.SinkErrorPolicyFail
	SinkErrorPolicyContinue = nodes.SinkErrorPolicyContinue
	SinkErrorPolicyRecord   = nodes.SinkErrorPolicyRecord
)

// Nodes package constructors
var (
	NewLLMNode                = nodes.NewLLMNode
	NewToolNode               = nodes.NewToolNode
	NewToolNodeWithRegistry   = nodes.NewToolNodeWithRegistry
	NewRuleRouter             = nodes.NewRuleRouter
	NewLLMRouter              = nodes.NewLLMRouter
	NewMergeNode              = nodes.NewMergeNode
	NewJSONMergeStrategy      = nodes.NewJSONMergeStrategy
	NewConcatMergeStrategy    = nodes.NewConcatMergeStrategy
	NewBestScoreMergeStrategy = nodes.NewBestScoreMergeStrategy
	NewFuncMergeStrategy      = nodes.NewFuncMergeStrategy
	NewAllMergeStrategy       = nodes.NewAllMergeStrategy
	NewMapNode                = nodes.NewMapNode
	NewFilterNode             = nodes.NewFilterNode
	NewTransformNode          = nodes.NewTransformNode
	NewGateNode               = nodes.NewGateNode
	NewCacheNode              = nodes.NewCacheNode
	NewMemoryCacheStore       = nodes.NewMemoryCacheStore
	NewCacheKeyBuilder        = nodes.NewCacheKeyBuilder
	NewMockNode               = nodes.NewMockNode
	NewGuardianNode           = nodes.NewGuardianNode
	NewHumanNode              = nodes.NewHumanNode
	NewChannelHumanHandler    = nodes.NewChannelHumanHandler
	NewCallbackHumanHandler   = nodes.NewCallbackHumanHandler
	NewAutoApproveHandler     = nodes.NewAutoApproveHandler
	NewAutoRejectHandler      = nodes.NewAutoRejectHandler
	NewQueuedHumanHandler     = nodes.NewQueuedHumanHandler
	NewSinkNode               = nodes.NewSinkNode
	NewMockHTTPClient         = nodes.NewMockHTTPClient
	NewMockMetricRecorder     = nodes.NewMockMetricRecorder
)

// =============================================================================
// Convenience type aliases for common function signatures
// =============================================================================

// EnvFunc is a convenience type for envelope transformation functions.
type EnvFunc = func(ctx context.Context, env *Envelope) (*Envelope, error)

// ConditionFunc is a convenience type for condition checking functions.
type ConditionFunc = func(ctx context.Context, env *Envelope) (bool, error)

// MergeFunc is a convenience type for custom merge functions.
type MergeFunc = func(ctx context.Context, inputs []*Envelope) (*Envelope, error)

// HumanCallbackFunc is a convenience type for human handler callbacks.
type HumanCallbackFunc = func(ctx context.Context, req *HumanRequest) (*HumanResponse, error)

// TransformFunc is a convenience type for custom transformation functions.
type TransformFunc = func(ctx context.Context, env *Envelope) (*Envelope, error)

// ValidateFunc is a convenience type for custom validation functions.
type ValidateFunc = func(ctx context.Context, env *Envelope, check *GuardianCheck) (bool, string, error)

// SinkWriteFunc is a convenience type for custom sink functions.
type SinkWriteFunc = func(ctx context.Context, env *Envelope, target *SinkTarget) error

// =============================================================================
// Convenience helper functions
// =============================================================================

// Run is a convenience function to execute a graph with default options.
// It creates a new runtime, runs the graph, and returns the result.
func Run(ctx context.Context, g Graph, env *Envelope) (*Envelope, error) {
	rt := NewRuntime()
	return rt.Run(ctx, g, env, DefaultRunOptions())
}

// RunWithOptions is a convenience function to execute a graph with custom options.
func RunWithOptions(ctx context.Context, g Graph, env *Envelope, opts RunOptions) (*Envelope, error) {
	rt := NewRuntime()
	return rt.Run(ctx, g, env, opts)
}

// RunWithHandler is a convenience function to execute a graph with an event handler.
func RunWithHandler(ctx context.Context, g Graph, env *Envelope, handler EventHandler) (*Envelope, error) {
	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.EventHandler = handler
	return rt.Run(ctx, g, env, opts)
}

// RunParallel is a convenience function to execute a graph with parallel execution.
func RunParallel(ctx context.Context, g Graph, env *Envelope, concurrency int) (*Envelope, error) {
	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.Concurrency = concurrency
	return rt.Run(ctx, g, env, opts)
}

// BuildGraph is a convenience function to create a simple linear graph.
// It creates a builder, adds all nodes with edges between them, and builds.
func BuildGraph(name string, nodes ...Node) (Graph, error) {
	if len(nodes) == 0 {
		return nil, ErrEmptyGraph
	}

	builder := NewGraphBuilder(name)
	builder.AddNode(nodes[0])
	for i := 1; i < len(nodes); i++ {
		builder.Edge(nodes[i])
	}
	return builder.Build()
}

// MustBuildGraph is like BuildGraph but panics on error.
// Useful in tests and examples.
func MustBuildGraph(name string, nodes ...Node) Graph {
	g, err := BuildGraph(name, nodes...)
	if err != nil {
		panic(err)
	}
	return g
}

// NewStepRunOptions creates RunOptions configured for step-through debugging.
func NewStepRunOptions(controller StepController) RunOptions {
	opts := DefaultRunOptions()
	opts.StepController = controller
	opts.StepConfig = DefaultStepConfig()
	return opts
}

// NewStepRunOptionsWithConfig creates RunOptions configured for step-through debugging
// with a custom step configuration.
func NewStepRunOptionsWithConfig(controller StepController, config *StepConfig) RunOptions {
	opts := DefaultRunOptions()
	opts.StepController = controller
	opts.StepConfig = config
	return opts
}

// NewTimedRunOptions creates RunOptions with a custom time function.
// Useful for testing time-sensitive behavior.
func NewTimedRunOptions(now func() time.Time) RunOptions {
	opts := DefaultRunOptions()
	opts.Now = now
	return opts
}
