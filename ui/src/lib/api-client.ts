import type {
  Workflow,
  Run,
  RunEvent,
  Tool,
  Provider,
  NodeType,
  ValidationResult,
  GraphDefinition,
  AgentWorkflow,
} from './api-types'

const BASE_URL = '/api'

class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
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
    const text = await response.text()
    // Check if we got an HTML response (likely a 404 page)
    if (text.startsWith('<!DOCTYPE') || text.startsWith('<html')) {
      throw new ApiError(response.status, `API endpoint not available: ${path}`)
    }
    throw new ApiError(response.status, text || `Request failed: ${response.statusText}`)
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

  validate: (source: string) =>
    request<ValidationResult>('/validate', {
      method: 'POST',
      body: source,
    }),

  compile: (source: string) =>
    request<GraphDefinition>('/compile', {
      method: 'POST',
      body: source,
    }),

  run: (id: string, input: Record<string, unknown>) =>
    request<Run>(`/workflows/${id}/run`, {
      method: 'POST',
      body: JSON.stringify({ input }),
    }),
}

// Runs API
export const runsApi = {
  list: (params?: { workflow_id?: string; status?: string }) => {
    const query = new URLSearchParams()
    if (params?.workflow_id) query.set('workflow_id', params.workflow_id)
    if (params?.status) query.set('status', params.status)
    const queryStr = query.toString()
    return request<Run[]>(`/runs${queryStr ? `?${queryStr}` : ''}`)
  },

  get: (runId: string) => request<Run>(`/runs/${runId}`),

  subscribeToEvents: (
    runId: string,
    onEvent: (event: RunEvent) => void,
    onError?: (error: Error) => void
  ): (() => void) => {
    const eventSource = new EventSource(`${BASE_URL}/runs/${runId}/events`)

    eventSource.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data) as RunEvent
        onEvent(event)
      } catch (err) {
        onError?.(err as Error)
      }
    }

    eventSource.onerror = () => {
      onError?.(new Error('Event stream connection failed'))
      eventSource.close()
    }

    return () => eventSource.close()
  },

  export: (runId: string) => {
    window.open(`${BASE_URL}/runs/${runId}/export`, '_blank')
  },
}

// Tools API
export const toolsApi = {
  list: () => request<Tool[]>('/tools'),

  get: (name: string) => request<Tool>(`/tools/${name}`),

  register: (tool: Partial<Tool>) =>
    request<Tool>('/tools', {
      method: 'POST',
      body: JSON.stringify(tool),
    }),

  update: (name: string, updates: Partial<Tool>) =>
    request<Tool>(`/tools/${name}`, {
      method: 'PUT',
      body: JSON.stringify(updates),
    }),

  delete: (name: string) =>
    request<void>(`/tools/${name}`, { method: 'DELETE' }),

  test: (name: string, action: string, input: Record<string, unknown>) =>
    request<Record<string, unknown>>(`/tools/${name}/test`, {
      method: 'POST',
      body: JSON.stringify({ action, input }),
    }),

  refresh: (name: string) =>
    request<Tool>(`/tools/${name}/refresh`, { method: 'POST' }),

  health: (name: string) =>
    request<{ status: string }>(`/tools/${name}/health`),
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
  list: () => request<NodeType[]>('/node-types'),
}

export { ApiError }
