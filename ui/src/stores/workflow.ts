import { create } from 'zustand'
import type { Workflow, AgentWorkflow, GraphDefinition, ValidationResult } from '@/lib/api-types'
import { workflowsApi } from '@/lib/api-client'

export interface WorkflowState {
  workflows: Workflow[]
  activeWorkflow: Workflow | null
  activeSource: string | Record<string, unknown> | null  // Can be string or parsed object from API
  isDirty: boolean
  loading: boolean
  error: string | null
  validationResult: ValidationResult | null
}

export interface WorkflowActions {
  fetchWorkflows: () => Promise<void>
  getWorkflow: (id: string) => Promise<Workflow>
  setActiveWorkflow: (workflow: Workflow | null) => void
  setActiveSource: (source: string) => void
  persistActiveWorkflow: () => Promise<boolean>
  markDirty: (dirty: boolean) => void
  createAgentWorkflow: (workflow: AgentWorkflow) => Promise<Workflow>
  createGraphWorkflow: (workflow: GraphDefinition) => Promise<Workflow>
  updateWorkflow: (id: string, source: string) => Promise<Workflow>
  deleteWorkflow: (id: string) => Promise<void>
  validateWorkflow: (source: string) => Promise<ValidationResult>
  compileWorkflow: (source: string) => Promise<GraphDefinition>
  clearError: () => void
}

const initialState: WorkflowState = {
  workflows: [],
  activeWorkflow: null,
  activeSource: null,
  isDirty: false,
  loading: false,
  error: null,
  validationResult: null,
}

function serializeSource(source: string | Record<string, unknown> | null): string | null {
  if (!source) {
    return null
  }
  return typeof source === 'string' ? source : JSON.stringify(source)
}

function asRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return {}
}

function asStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return []
  }
  return value.filter((entry): entry is string => typeof entry === 'string')
}

function asFiniteNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function normalizeAgentWorkflowSource(sourceStr: string): string {
  try {
    const parsed = asRecord(JSON.parse(sourceStr))
    if (parsed.kind !== 'agent_workflow') {
      return sourceStr
    }

    const tasks = asRecord(parsed.tasks)
    const taskIDs = Object.keys(tasks)

    const agents = asRecord(parsed.agents)
    const normalizedAgents: Record<string, unknown> = {}
    for (const [agentID, rawAgent] of Object.entries(agents)) {
      const agent = asRecord(rawAgent)
      const normalizedAgent: Record<string, unknown> = { ...agent }
      const config = asRecord(agent.config)

      const temperature = asFiniteNumber(agent.temperature)
      if (temperature !== null && asFiniteNumber(config.temperature) === null) {
        config.temperature = temperature
      }

      const maxTokens = asFiniteNumber(agent.max_tokens)
      if (maxTokens !== null && asFiniteNumber(config.max_tokens) === null) {
        config.max_tokens = maxTokens
      }

      if (Object.keys(config).length > 0) {
        normalizedAgent.config = config
      }
      normalizedAgents[agentID] = normalizedAgent
    }

    const execution = asRecord(parsed.execution)
    const strategy = typeof execution.strategy === 'string' ? execution.strategy : ''
    const normalizedExecution: Record<string, unknown> = { ...execution }

    if (strategy === 'sequential') {
      const existingOrder = asStringArray(execution.task_order)
      const seen = new Set<string>()
      const ordered = existingOrder.filter((taskID) => {
        if (!taskIDs.includes(taskID) || seen.has(taskID)) {
          return false
        }
        seen.add(taskID)
        return true
      })
      const missing = taskIDs.filter((taskID) => !seen.has(taskID))
      normalizedExecution.task_order = [...ordered, ...missing]
    }

    if (strategy === 'custom') {
      const executionTasks = asRecord(execution.tasks)
      const normalizedExecutionTasks: Record<string, unknown> = {}
      for (const taskID of taskIDs) {
        const rawExecutionTask = asRecord(executionTasks[taskID])
        const dependsOn = asStringArray(rawExecutionTask.depends_on).filter(
          (depID) => taskIDs.includes(depID) && depID !== taskID
        )
        normalizedExecutionTasks[taskID] =
          typeof rawExecutionTask.condition === 'string' && rawExecutionTask.condition.trim() !== ''
            ? { depends_on: dependsOn, condition: rawExecutionTask.condition }
            : { depends_on: dependsOn }
      }
      normalizedExecution.tasks = normalizedExecutionTasks
    }

    return JSON.stringify({
      ...parsed,
      agents: normalizedAgents,
      execution: normalizedExecution,
    })
  } catch {
    return sourceStr
  }
}

function normalizeSourceForComparison(source: string | Record<string, unknown> | null): string | null {
  const serialized = serializeSource(source)
  if (!serialized) {
    return null
  }

  try {
    return JSON.stringify(JSON.parse(serialized))
  } catch {
    return serialized
  }
}

export const useWorkflowStore = create<WorkflowState & WorkflowActions>()((set, get) => ({
  ...initialState,

  fetchWorkflows: async () => {
    set({ loading: true, error: null })
    try {
      const response = await workflowsApi.list()
      // Defensive: ensure workflows is always an array
      const workflows = Array.isArray(response) ? response : []
      set({ workflows, loading: false })
    } catch (err) {
      set({ error: (err as Error).message, loading: false, workflows: [] })
    }
  },

  getWorkflow: async (id: string) => {
    set({ loading: true, error: null })
    try {
      const workflow = await workflowsApi.get(id)
      set({ loading: false })
      return workflow
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  setActiveWorkflow: (workflow) => {
    set({
      activeWorkflow: workflow,
      activeSource: workflow?.source || null,
      isDirty: false,
      validationResult: null,
    })
  },

  setActiveSource: (source) => {
    set((state) => {
      const activeWorkflowSource = normalizeSourceForComparison(
        state.activeWorkflow?.source as string | Record<string, unknown> | null
      )
      const nextSource = normalizeSourceForComparison(source)
      return {
        activeSource: source,
        isDirty: activeWorkflowSource !== nextSource,
      }
    })
  },

  persistActiveWorkflow: async () => {
    const state = get()
    if (!state.activeWorkflow || !state.activeSource || !state.isDirty) {
      return true
    }

    const sourceStr = serializeSource(state.activeSource)
    if (!sourceStr) {
      return true
    }

    const normalizedSourceStr = normalizeAgentWorkflowSource(sourceStr)

    const validation = await get().validateWorkflow(normalizedSourceStr)
    if (!validation.valid) {
      return false
    }

    await get().updateWorkflow(state.activeWorkflow.id, normalizedSourceStr)
    return true
  },

  markDirty: (dirty) => {
    set({ isDirty: dirty })
  },

  createAgentWorkflow: async (workflow) => {
    set({ loading: true, error: null })
    try {
      const created = await workflowsApi.createAgent(workflow)
      set((state) => ({
        workflows: [...state.workflows, created],
        loading: false,
      }))
      return created
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  createGraphWorkflow: async (workflow) => {
    set({ loading: true, error: null })
    try {
      const created = await workflowsApi.createGraph(workflow)
      set((state) => ({
        workflows: [...state.workflows, created],
        loading: false,
      }))
      return created
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  updateWorkflow: async (id, source) => {
    set({ loading: true, error: null })
    try {
      const updated = await workflowsApi.update(id, source)
      set((state) => ({
        workflows: state.workflows.map((w) => (w.id === id ? updated : w)),
        activeWorkflow: state.activeWorkflow?.id === id ? updated : state.activeWorkflow,
        activeSource: state.activeWorkflow?.id === id ? updated.source : state.activeSource,
        isDirty: false,
        loading: false,
      }))
      return updated
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  deleteWorkflow: async (id) => {
    set({ loading: true, error: null })
    try {
      await workflowsApi.delete(id)
      set((state) => ({
        workflows: state.workflows.filter((w) => w.id !== id),
        activeWorkflow: state.activeWorkflow?.id === id ? null : state.activeWorkflow,
        activeSource: state.activeWorkflow?.id === id ? null : state.activeSource,
        loading: false,
      }))
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  validateWorkflow: async (source) => {
    try {
      const result = await workflowsApi.validate(source)
      set({ validationResult: result })
      return result
    } catch (err) {
      const result: ValidationResult = {
        valid: false,
        diagnostics: [{ severity: 'error', message: (err as Error).message }],
      }
      set({ validationResult: result })
      return result
    }
  },

  compileWorkflow: async (source) => {
    return workflowsApi.compile(source)
  },

  clearError: () => set({ error: null }),
}))
