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

export const useWorkflowStore = create<WorkflowState & WorkflowActions>()((set) => ({
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
    set({ activeSource: source, isDirty: true })
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
