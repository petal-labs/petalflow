/** Standard daemon error envelope. */
export interface ApiErrorBody {
  error: {
    code: string
    message: string
    details?: string[]
  }
}

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

export interface AuthStatus {
  setup_complete: boolean
}

export interface AuthTokens {
  access_token: string
  refresh_token: string
  expires_in: number
}

export interface LoginRequest {
  username: string
  password: string
}

export interface SetupRequest {
  username: string
  password: string
}

export interface ChangePasswordRequest {
  current_password: string
  new_password: string
}

// ---------------------------------------------------------------------------
// Health
// ---------------------------------------------------------------------------

export interface HealthResponse {
  status: string
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

export interface Provider {
  name: string
  default_model: string
  base_url?: string
  /** True once a test connection has succeeded. */
  verified: boolean
  /** Latency in ms from last test, if available. */
  latency_ms?: number
}

export interface ProviderCreateRequest {
  name: string
  api_key: string
  default_model: string
  base_url?: string
  organization_id?: string
  project_id?: string
}

export interface ProviderUpdateRequest {
  api_key?: string
  default_model?: string
  base_url?: string
  organization_id?: string
  project_id?: string
}

export interface ProviderTestResult {
  success: boolean
  latency_ms: number
  error?: string
}

// ---------------------------------------------------------------------------
// Tools
// ---------------------------------------------------------------------------

export type ToolStatus = "ready" | "unhealthy" | "disabled" | "unverified"
export type ToolTransport = "stdio" | "sse" | "http" | "in-proc"

export interface ToolAction {
  name: string
  description?: string
  input_schema?: Record<string, unknown>
  output_schema?: Record<string, unknown>
}

export interface Tool {
  name: string
  type: string
  transport: ToolTransport
  status: ToolStatus
  description?: string
  version?: string
  author?: string
  actions: ToolAction[]
}

export interface ToolRegisterRequest {
  name: string
  type: string
  transport: ToolTransport
  command?: string
  args?: string[]
  endpoint_url?: string
  env?: Record<string, string>
  timeout_ms?: number
}

export interface ToolHealthResult {
  status: ToolStatus
  latency_ms: number
  checked_at: string
  error?: string
}

// ---------------------------------------------------------------------------
// Workflows
// ---------------------------------------------------------------------------

export type WorkflowKind = "agent_workflow" | "graph"

export interface WorkflowSummary {
  id: string
  name: string
  description?: string
  kind: WorkflowKind
  tags?: string[]
  created_at: string
  updated_at: string
  /** Stats derived from the workflow definition. */
  agent_count?: number
  task_count?: number
  node_count?: number
  edge_count?: number
}

export interface Workflow extends WorkflowSummary {
  /** The raw workflow definition (agent_workflow or graph JSON). */
  definition: Record<string, unknown>
  /** The compiled graph IR (if agent_workflow). */
  compiled?: Record<string, unknown>
}

export interface WorkflowCreateRequest {
  name: string
  kind: WorkflowKind
  definition: Record<string, unknown>
  description?: string
  tags?: string[]
}

export interface WorkflowUpdateRequest {
  name?: string
  definition?: Record<string, unknown>
  description?: string
  tags?: string[]
}

export interface ValidationResult {
  valid: boolean
  diagnostics: ValidationDiagnostic[]
}

export interface ValidationDiagnostic {
  severity: "error" | "warning" | "info"
  message: string
  path?: string
}

export interface CompileResult {
  graph: Record<string, unknown>
  diagnostics?: ValidationDiagnostic[]
}

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

export type RunStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "cancelled"

export interface RunSummary {
  run_id: string
  workflow_id: string
  status: RunStatus
  started_at: string
  completed_at?: string
  duration_ms?: number
}

export interface Run extends RunSummary {
  inputs?: Record<string, unknown>
  outputs?: Record<string, unknown>
  error?: { code: string; message: string }
  tokens_in?: number
  tokens_out?: number
}

export interface RunStartRequest {
  inputs?: Record<string, unknown>
  trace?: boolean
  dry_run?: boolean
}

export interface ReviewRequest {
  action: "approve" | "reject"
  feedback?: string
}

/** WebSocket event types from WS /api/runs/{run_id}/stream. */
export type RunEvent =
  | { type: "node_started"; node_id: string; timestamp: string }
  | {
      type: "node_output"
      node_id: string
      chunk: string
      stream: boolean
    }
  | {
      type: "node_completed"
      node_id: string
      timestamp: string
      duration_ms: number
      outputs: Record<string, unknown>
    }
  | {
      type: "node_failed"
      node_id: string
      error: { code: string; message: string }
    }
  | {
      type: "node_review_required"
      node_id: string
      gate_id: string
      instructions: string
    }
  | {
      type: "run_completed"
      timestamp: string
      duration_ms: number
      final_outputs: Record<string, unknown>
    }
  | {
      type: "run_failed"
      timestamp: string
      error: { code: string; message: string }
    }
  | { type: "trace_event"; node_id: string; event: Record<string, unknown> }

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

export interface AppSettings {
  onboarding_complete: boolean
  onboarding_step?: number
  preferences: UserPreferences
}

export interface UserPreferences {
  default_workflow_mode?: "agent_task" | "graph"
  auto_save_interval_ms?: number
  tracing_default?: boolean
  theme?: "light" | "dark" | "system"
  snap_to_grid?: boolean
  show_port_types?: boolean
  output_format?: "markdown" | "plain" | "json"
}

// ---------------------------------------------------------------------------
// Node types (for graph mode palette)
// ---------------------------------------------------------------------------

export interface NodeType {
  kind: string
  category: string
  description?: string
  input_ports: PortDef[]
  output_ports: PortDef[]
  config_schema?: Record<string, unknown>
}

export interface PortDef {
  name: string
  type: string
  required?: boolean
  description?: string
}
