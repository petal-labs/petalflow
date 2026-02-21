// API types matching the Go server responses

export type WorkflowKind = 'agent_workflow' | 'graph'

export interface Workflow {
  id: string
  name: string
  kind: WorkflowKind
  source: string | Record<string, unknown> // Raw JSON - can be string or object from API
  compiled?: GraphDefinition
  created_at: string
  updated_at: string
}

export interface GraphDefinition {
  id: string
  version: string
  metadata?: Record<string, string>
  nodes: NodeDef[]
  edges: EdgeDef[]
  entry: string
}

export interface NodeDef {
  id: string
  type: string
  config: Record<string, unknown>
}

export interface EdgeDef {
  source: string
  source_handle: string
  target: string
  target_handle: string
}

// Agent/Task workflow schema
export interface AgentWorkflow {
  version: string
  kind: 'agent_workflow'
  id: string
  name: string
  agents: Record<string, Agent>
  tasks: Record<string, Task>
  execution: ExecutionConfig
}

export interface Agent {
  id: string
  role: string
  goal: string
  backstory?: string
  provider: string
  model: string
  tools?: string[]
  temperature?: number
  max_tokens?: number
}

export interface Task {
  id: string
  description: string
  agent: string
  expected_output?: string
  output_key?: string
  depends_on?: string[]
  inputs?: Record<string, string>
}

export interface ExecutionConfig {
  strategy: 'sequential' | 'parallel' | 'hierarchical' | 'custom'
  task_order?: string[]
}

// Run types
export type RunStatus = 'running' | 'success' | 'failed' | 'canceled'

export interface Run {
  run_id: string
  workflow_id: string
  status: RunStatus
  input: Record<string, unknown>
  output?: Record<string, unknown>
  metrics?: RunMetrics
  trace_id?: string
  started_at: string
  finished_at?: string
}

export interface RunMetrics {
  duration_ms: number
  total_tokens: number
  tool_calls: number
}

export interface RunEvent {
  id: number
  run_id: string
  event_type: string
  node_id?: string
  payload: Record<string, unknown>
  timestamp: string
}

// Tool types
export type ToolOrigin = 'native' | 'mcp' | 'http' | 'stdio'
export type ToolStatus = 'ready' | 'unhealthy' | 'disabled'

export interface Tool {
  name: string
  origin: ToolOrigin
  manifest: ToolManifest
  config?: Record<string, unknown>
  status: ToolStatus
  overlay?: Record<string, unknown>
  registered_at: string
  last_health_check?: string
}

export interface ToolManifest {
  name: string
  description?: string
  version?: string
  actions: ToolAction[]
}

export interface ToolAction {
  name: string
  description?: string
  parameters?: Record<string, unknown>
}

// Provider types
export type ProviderType = 'anthropic' | 'openai' | 'google' | 'ollama'
export type ProviderStatus = 'connected' | 'disconnected' | 'error'

export interface Provider {
  id: string
  type: ProviderType
  name: string
  default_model?: string
  status?: ProviderStatus
  created_at: string
}

// Node type (for graph designer palette)
export interface NodeType {
  type: string
  kind: string
  category: string
  display_name: string
  description?: string
  config_schema?: Record<string, unknown>
  inputs?: PortDef[]
  outputs?: PortDef[]
}

export interface PortDef {
  name: string
  type: string
  required?: boolean
  description?: string
}

// Validation
export interface ValidationResult {
  valid: boolean
  diagnostics: Diagnostic[]
}

export interface Diagnostic {
  severity: 'error' | 'warning' | 'info'
  message: string
  path?: string
  code?: string
}

// API response wrappers
export interface ApiResponse<T> {
  data?: T
  error?: string
}

export interface ListResponse<T> {
  items: T[]
  total?: number
}
