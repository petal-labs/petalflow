import type {
  Workflow,
  Run,
  RunEvent,
  RunExport,
  RunStatus,
  Tool,
  ToolAction,
  ToolOrigin,
  ToolStatus,
  Provider,
  NodeType,
  ValidationResult,
  GraphDefinition,
  AgentWorkflow,
} from './api-types'

const BASE_URL = '/api'
const KNOWN_SSE_EVENTS = [
  'run.started',
  'run.finished',
  'run.failed',
  'run.error',
  'node.started',
  'node.finished',
  'node.failed',
  'node.output',
  'node.output.delta',
  'node.output.final',
  'node.output.preview',
  'route.decision',
  'step.paused',
  'step.resumed',
  'step.skipped',
  'step.aborted',
  'tool.call',
  'tool.result',
  'run.snapshot',
] as const

class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

async function readErrorResponseText(response: Response): Promise<string> {
  try {
    return await response.text()
  } catch {
    return ''
  }
}

async function throwApiResponseError(response: Response, path: string): Promise<never> {
  const text = await readErrorResponseText(response)
  if (text.startsWith('<!DOCTYPE') || text.startsWith('<html')) {
    throw new ApiError(response.status, `API endpoint not available: ${path}`)
  }
  throw new ApiError(response.status, text || `Request failed: ${response.statusText}`)
}

async function request<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const response = await fetch(`${BASE_URL}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options.headers,
    },
  })

  if (!response.ok) {
    await throwApiResponseError(response, path)
  }

  if (response.status === 204) {
    return undefined as T
  }

  // Check content type to avoid parsing HTML as JSON
  const contentType = response.headers.get('content-type')
  if (contentType && !contentType.includes('application/json')) {
    throw new ApiError(response.status, `Expected JSON response but got: ${contentType}`)
  }

  return response.json()
}

interface SSEFrame {
  event: string
  data: string
}

function parseSSEFrames(buffer: string): { frames: SSEFrame[]; rest: string } {
  const frames: SSEFrame[] = []
  let rest = buffer.replace(/\r/g, '')

  while (true) {
    const delimiterIndex = rest.indexOf('\n\n')
    if (delimiterIndex < 0) {
      break
    }

    const rawFrame = rest.slice(0, delimiterIndex)
    rest = rest.slice(delimiterIndex + 2)
    if (!rawFrame.trim()) {
      continue
    }

    let event = 'message'
    const dataLines: string[] = []
    for (const rawLine of rawFrame.split('\n')) {
      const line = rawLine.trimEnd()
      if (!line || line.startsWith(':')) {
        continue
      }
      if (line.startsWith('event:')) {
        event = line.slice('event:'.length).trim()
        continue
      }
      if (line.startsWith('data:')) {
        dataLines.push(line.slice('data:'.length).trim())
      }
    }

    if (dataLines.length === 0) {
      continue
    }

    frames.push({
      event,
      data: dataLines.join('\n'),
    })
  }

  return { frames, rest }
}

function parseJSONOrString(value: string): unknown {
  try {
    return JSON.parse(value) as unknown
  } catch {
    return value
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms)
  })
}

async function waitForRunCompletion(
  runID: string,
  workflowIDFallback: string,
  timeoutSeconds: number
): Promise<Run> {
  const timeoutMs = Math.max(5, Math.floor(timeoutSeconds)) * 1000
  const deadline = Date.now() + timeoutMs
  let lastRun: Run | null = null

  while (Date.now() < deadline) {
    try {
      const run = normalizeRun(await request<unknown>(`/runs/${runID}`), workflowIDFallback)
      lastRun = run
      if (run.status !== 'running') {
        return run
      }
    } catch {
      // Run can be temporarily unavailable while the event store catches up.
    }
    await sleep(1000)
  }

  if (lastRun) {
    return lastRun
  }
  throw new ApiError(504, `Timed out waiting for run ${runID}`)
}

function asObject(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return {}
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function asNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value
  }
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) {
      return parsed
    }
  }
  return undefined
}

function normalizeRunStatus(raw: unknown): RunStatus {
  const status = asString(raw).toLowerCase()
  if (status === 'completed') return 'completed'
  if (status === 'success') return 'success'
  if (status === 'running') return 'running'
  if (status === 'failed') return 'failed'
  if (status === 'canceled') return 'canceled'
  return 'failed'
}

function normalizeRun(raw: unknown, workflowIDFallback = ''): Run {
  const data = asObject(raw)
  const runID = asString(data.run_id || data.RunID)
  const workflowID = asString(data.workflow_id || data.WorkflowID || data.id || data.ID) || workflowIDFallback

  const output = asObject(data.output || data.Output)
  const input = asObject(data.input || data.Input)
  const startedAt = asString(data.started_at || data.StartedAt) || new Date().toISOString()
  const completedAt = asString(data.completed_at || data.CompletedAt)
  const finishedAt = asString(data.finished_at || data.FinishedAt) || completedAt
  const durationMs = asNumber(data.duration_ms || data.DurationMs)

  return {
    id: asString(data.id || data.ID),
    run_id: runID,
    workflow_id: workflowID,
    status: normalizeRunStatus(data.status || data.Status),
    input,
    output: Object.keys(output).length > 0 ? output : undefined,
    started_at: startedAt,
    finished_at: finishedAt || undefined,
    completed_at: completedAt || undefined,
    duration_ms: durationMs,
  }
}

function normalizeRunEvent(raw: unknown, fallbackType = 'message'): RunEvent {
  const data = asObject(raw)
  const payload = asObject(data.payload || data.Payload)
  const eventType = asString(data.event_type || data.Kind) || fallbackType
  const id = asNumber(data.id || data.seq || data.Seq) ?? Date.now()

  return {
    id,
    run_id: asString(data.run_id || data.RunID),
    event_type: eventType,
    node_id: asString(data.node_id || data.NodeID) || undefined,
    payload,
    timestamp: asString(data.timestamp || data.Time) || new Date().toISOString(),
    trace_id: asString(data.trace_id || data.TraceID) || undefined,
    span_id: asString(data.span_id || data.SpanID) || undefined,
  }
}

function normalizeRunExport(raw: unknown): RunExport {
  const data = asObject(raw)
  const eventsRaw = data.events
  const events = Array.isArray(eventsRaw)
    ? eventsRaw.map((event) => {
        const eventObject = asObject(event)
        const eventType = asString(eventObject.event_type || eventObject.Kind) || 'event'
        return normalizeRunEvent(event, eventType)
      })
    : []

  return {
    run: normalizeRun(data.run),
    events,
  }
}

function normalizeToolStatus(raw: unknown): ToolStatus {
  const status = asString(raw).toLowerCase()
  if (status === 'ready') return 'ready'
  if (status === 'unhealthy') return 'unhealthy'
  if (status === 'disabled') return 'disabled'
  return 'unverified'
}

function normalizeToolOrigin(raw: unknown): ToolOrigin {
  const origin = asString(raw).toLowerCase()
  if (origin === 'mcp' || origin === 'http' || origin === 'stdio') {
    return origin
  }
  return 'native'
}

function normalizeToolActions(raw: unknown): ToolAction[] {
  if (Array.isArray(raw)) {
    return raw
      .map((entry) => asObject(entry))
      .map((entry) => ({
        name: asString(entry.name || entry.Name),
        description: asString(entry.description || entry.Description) || undefined,
        parameters: asObject(entry.parameters || entry.inputs || entry.Inputs || entry.Parameters),
        inputs: asObject(entry.inputs || entry.Inputs),
        outputs: asObject(entry.outputs || entry.Outputs),
      }))
      .filter((entry) => entry.name !== '')
  }

  const actionsObject = asObject(raw)
  return Object.entries(actionsObject).map(([name, spec]) => {
    const mapped = asObject(spec)
    return {
      name,
      description: asString(mapped.description || mapped.Description) || undefined,
      parameters: asObject(mapped.inputs || mapped.Inputs || mapped.parameters || mapped.Parameters),
      inputs: asObject(mapped.inputs || mapped.Inputs),
      outputs: asObject(mapped.outputs || mapped.Outputs),
    }
  })
}

function normalizeTool(raw: unknown): Tool {
  const data = asObject(raw)
  const manifestRaw = asObject(data.manifest || data.Manifest)
  const toolMeta = asObject(manifestRaw.tool || manifestRaw.Tool)
  const transport = asObject(manifestRaw.transport || manifestRaw.Transport)
  const actions = normalizeToolActions(manifestRaw.actions || manifestRaw.Actions)
  const manifestVersion = asString(manifestRaw.manifest_version || manifestRaw.ManifestVersion)

  return {
    name: asString(data.name || data.Name || toolMeta.name || toolMeta.Name),
    origin: normalizeToolOrigin(data.origin || data.Origin),
    manifest: {
      name: asString(toolMeta.name || toolMeta.Name || data.name || data.Name),
      description: asString(toolMeta.description || toolMeta.Description) || undefined,
      version: asString(toolMeta.version || toolMeta.Version || manifestVersion) || undefined,
      actions,
      transport: {
        type: asString(transport.type || transport.Type) || undefined,
        endpoint: asString(transport.endpoint || transport.Endpoint) || undefined,
        command: asString(transport.command || transport.Command) || undefined,
        mode: asString(transport.mode || transport.Mode) || undefined,
      },
    },
    config: asObject(data.config || data.Config) as Record<string, string>,
    status: normalizeToolStatus(data.status || data.Status),
    overlay: asObject(data.overlay || data.Overlay),
    registered_at: asString(data.registered_at || data.RegisteredAt),
    last_health_check: asString(data.last_health_check || data.LastHealthCheck) || undefined,
  }
}

function normalizeNodeType(raw: unknown): NodeType {
  const data = asObject(raw)
  const ports = asObject(data.ports || data.Ports)
  const inputs = (Array.isArray(data.inputs) ? data.inputs : ports.inputs) || []
  const outputs = (Array.isArray(data.outputs) ? data.outputs : ports.outputs) || []
  return {
    type: asString(data.type || data.Type),
    category: asString(data.category || data.Category),
    display_name: asString(data.display_name || data.DisplayName),
    description: asString(data.description || data.Description) || undefined,
    config_schema: asObject(data.config_schema || data.ConfigSchema),
    ports: {
      inputs: Array.isArray(inputs) ? inputs : [],
      outputs: Array.isArray(outputs) ? outputs : [],
    },
    inputs: Array.isArray(inputs) ? inputs : [],
    outputs: Array.isArray(outputs) ? outputs : [],
    is_tool: Boolean(data.is_tool || data.IsTool),
    tool_mode: asString(data.tool_mode || data.ToolMode) || undefined,
  }
}

export interface RunStartOptions {
  stream?: boolean
  timeoutSeconds?: number
  humanMode?: 'strict' | 'auto_approve' | 'auto_reject'
}

// Workflows API
export const workflowsApi = {
  list: () => request<Workflow[]>('/workflows'),

  get: (id: string) => request<Workflow>(`/workflows/${id}`),

  createAgent: (workflow: AgentWorkflow) =>
    request<Workflow>('/workflows/agent', {
      method: 'POST',
      body: JSON.stringify(workflow),
    }),

  createGraph: (workflow: GraphDefinition) =>
    request<Workflow>('/workflows/graph', {
      method: 'POST',
      body: JSON.stringify(workflow),
    }),

  update: (id: string, source: string) =>
    request<Workflow>(`/workflows/${id}`, {
      method: 'PUT',
      body: source,
    }),

  delete: (id: string) =>
    request<void>(`/workflows/${id}`, { method: 'DELETE' }),

  validate: async (source: string) => {
    try {
      JSON.parse(source)
      return {
        valid: true,
        diagnostics: [],
      } satisfies ValidationResult
    } catch (err) {
      return {
        valid: false,
        diagnostics: [
          {
            severity: 'error',
            message: `Invalid JSON source: ${(err as Error).message}`,
          },
        ],
      } satisfies ValidationResult
    }
  },

  compile: async (_source: string) => {
    throw new ApiError(
      501,
      'Compile endpoint is not available. Workflows are compiled on create/update.'
    )
  },

  run: async (
    id: string,
    input: Record<string, unknown>,
    options?: RunStartOptions
  ) => {
    const timeoutSeconds =
      typeof options?.timeoutSeconds === 'number' && Number.isFinite(options.timeoutSeconds)
        ? Math.max(1, Math.floor(options.timeoutSeconds))
        : 300

    const runOptions: Record<string, unknown> = {}
    if (typeof options?.stream === 'boolean') {
      runOptions.stream = options.stream
    }
    if (typeof options?.timeoutSeconds === 'number' && Number.isFinite(options.timeoutSeconds)) {
      runOptions.timeout = `${timeoutSeconds}s`
    }
    if (options?.humanMode) {
      runOptions.human = { mode: options.humanMode }
    }

    const body: Record<string, unknown> = { input }
    if (Object.keys(runOptions).length > 0) {
      body.options = runOptions
    }

    if (runOptions.stream === true) {
      const path = `/workflows/${id}/run`
      const response = await fetch(`${BASE_URL}${path}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!response.ok) {
        await throwApiResponseError(response, path)
      }

      const contentType = response.headers.get('content-type') || ''
      if (!contentType.includes('text/event-stream')) {
        const rawRun = await response.json() as Record<string, unknown>
        return normalizeRun(rawRun, id)
      }
      if (!response.body) {
        throw new ApiError(500, 'Run stream body was empty')
      }

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      let runID = ''
      let runtimeError = ''

      let streamReadError: unknown = null
      try {
        while (true) {
          const { value, done } = await reader.read()
          if (done) {
            break
          }

          buffer += decoder.decode(value, { stream: true })
          const parsed = parseSSEFrames(buffer)
          buffer = parsed.rest
          for (const frame of parsed.frames) {
            const payload = parseJSONOrString(frame.data)
            const data = asObject(payload)
            if (!runID) {
              const candidateRunID = asString(data.run_id || data.RunID)
              if (candidateRunID) {
                runID = candidateRunID
              }
            }

            if (frame.event === 'run.error' || frame.event === 'run.failed') {
              runtimeError = asString(data.error || data.message) || asString(payload)
            }
          }
        }
      } catch (err) {
        streamReadError = err

        if (runID) {
          return waitForRunCompletion(runID, id, timeoutSeconds)
        }
      } finally {
        reader.releaseLock()
      }

      if (runtimeError) {
        throw new ApiError(500, runtimeError)
      }
      if (streamReadError) {
        const fallbackOptions = { ...runOptions, stream: false }
        const fallbackBody: Record<string, unknown> = { input }
        if (Object.keys(fallbackOptions).length > 0) {
          fallbackBody.options = fallbackOptions
        }
        const rawFallbackRun = await request<Record<string, unknown>>(`/workflows/${id}/run`, {
          method: 'POST',
          body: JSON.stringify(fallbackBody),
        })
        return normalizeRun(rawFallbackRun, id)
      }
      if (!runID) {
        throw new ApiError(500, 'Run stream completed without run_id')
      }

      return waitForRunCompletion(runID, id, timeoutSeconds)
    }

    const rawRun = await request<Record<string, unknown>>(`/workflows/${id}/run`, {
      method: 'POST',
      body: JSON.stringify(body),
    })
    return normalizeRun(rawRun, id)
  },
}

// Runs API
export const runsApi = {
  list: async (params?: { workflow_id?: string; status?: string }) => {
    const query = new URLSearchParams()
    if (params?.workflow_id) query.set('workflow_id', params.workflow_id)
    if (params?.status) query.set('status', params.status)
    const queryStr = query.toString()
    const response = await request<unknown[]>(`/runs${queryStr ? `?${queryStr}` : ''}`)
    if (!Array.isArray(response)) {
      return []
    }
    return response.map((entry) => normalizeRun(entry))
  },

  get: async (runId: string) => {
    const response = await request<unknown>(`/runs/${runId}`)
    return normalizeRun(response)
  },

  export: async (runId: string) => {
    const response = await request<unknown>(`/runs/${runId}/export`)
    return normalizeRunExport(response)
  },

  subscribeToEvents: (
    runId: string,
    onEvent: (event: RunEvent) => void,
    onError?: (error: Error) => void
  ): (() => void) => {
    const eventSource = new EventSource(`${BASE_URL}/runs/${runId}/events`)

    const handleEvent = (eventType: string, data: string) => {
      try {
        const parsed = JSON.parse(data) as unknown
        const event = normalizeRunEvent(parsed, eventType)
        onEvent(event)
      } catch (err) {
        onError?.(err as Error)
      }
    }

    for (const eventType of KNOWN_SSE_EVENTS) {
      eventSource.addEventListener(eventType, (event) => {
        const message = event as MessageEvent<string>
        handleEvent(eventType, message.data)
      })
    }

    eventSource.onmessage = (e) => {
      handleEvent('message', e.data)
    }

    eventSource.onerror = () => {
      if (eventSource.readyState !== EventSource.CLOSED) {
        onError?.(new Error('Event stream connection failed'))
      }
    }

    return () => eventSource.close()
  },
}

// Tools API
export const toolsApi = {
  list: async () => {
    const response = await request<Record<string, unknown> | unknown[]>('/tools')
    const toolsRaw = Array.isArray(response) ? response : response.tools
    if (!Array.isArray(toolsRaw)) {
      return []
    }
    return toolsRaw.map((entry) => normalizeTool(entry))
  },

  get: async (name: string) => {
    const response = await request<Record<string, unknown>>(`/tools/${name}`)
    return normalizeTool(response)
  },

  register: async (tool: Record<string, unknown>) => {
    const response = await request<Record<string, unknown>>('/tools', {
      method: 'POST',
      body: JSON.stringify(tool),
    })
    return normalizeTool(response)
  },

  update: async (name: string, updates: Record<string, unknown>) => {
    const response = await request<Record<string, unknown>>(`/tools/${name}`, {
      method: 'PUT',
      body: JSON.stringify(updates),
    })
    return normalizeTool(response)
  },

  delete: (name: string) =>
    request<void>(`/tools/${name}`, { method: 'DELETE' }),

  test: (name: string, action: string, input: Record<string, unknown>) =>
    request<Record<string, unknown>>(`/tools/${name}/test`, {
      method: 'POST',
      body: JSON.stringify({ action, inputs: input }),
    }),

  refresh: async (name: string) => {
    const response = await request<Record<string, unknown>>(`/tools/${name}/refresh`, { method: 'POST' })
    return normalizeTool(response)
  },

  health: async (name: string) => {
    const response = await request<Record<string, unknown>>(`/tools/${name}/health`)
    const health = asObject(response.health)
    const state = asString(health.state || health.State).toLowerCase()
    if (state === 'healthy') {
      return { status: 'ready' as const }
    }
    if (state === 'unhealthy') {
      return { status: 'unhealthy' as const }
    }
    return { status: 'unverified' as const }
  },
}

// Providers API
export const providersApi = {
  list: () => request<Provider[]>('/providers'),

  add: (provider: Omit<Provider, 'id' | 'created_at'> & { api_key?: string }) =>
    request<Provider>('/providers', {
      method: 'POST',
      body: JSON.stringify(provider),
    }),

  update: (id: string, updates: Partial<Provider>) =>
    request<Provider>(`/providers/${id}`, {
      method: 'PUT',
      body: JSON.stringify(updates),
    }),

  delete: (id: string) =>
    request<void>(`/providers/${id}`, { method: 'DELETE' }),

  test: (id: string) =>
    request<{ success: boolean; models?: string[] }>(`/providers/${id}/test`, {
      method: 'POST',
    }),
}

// Node types API
export const nodeTypesApi = {
  list: async () => {
    const response = await request<Record<string, unknown> | unknown[]>('/node-types')
    const nodeTypesRaw = Array.isArray(response) ? response : response.node_types
    if (!Array.isArray(nodeTypesRaw)) {
      return []
    }
    return nodeTypesRaw.map((nodeType) => normalizeNodeType(nodeType))
  },
}

export { ApiError }
