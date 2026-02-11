import { create } from "zustand"
import { api } from "@/api/client"
import type {
  Workflow,
  WorkflowSummary,
  WorkflowCreateRequest,
  WorkflowUpdateRequest,
  ValidationResult,
  CompileResult,
} from "@/api/types"

interface WorkflowState {
  /** List of workflow summaries for the library. */
  workflows: WorkflowSummary[]
  loading: boolean

  /** Currently open workflow in the editor (null if none). */
  current: Workflow | null
  /** Whether the editor has unsaved changes. */
  dirty: boolean
  /** Saving indicator. */
  saving: boolean

  fetchWorkflows: () => Promise<void>
  getWorkflow: (id: string) => Promise<Workflow>
  createWorkflow: (req: WorkflowCreateRequest) => Promise<WorkflowSummary>
  updateWorkflow: (id: string, req: WorkflowUpdateRequest) => Promise<void>
  deleteWorkflow: (id: string) => Promise<void>
  duplicateWorkflow: (id: string) => Promise<WorkflowSummary>

  /** Validate without saving. */
  validate: (definition: Record<string, unknown>) => Promise<ValidationResult>
  /** Compile Agent/Task → Graph IR (preview, no save). */
  compile: (definition: Record<string, unknown>) => Promise<CompileResult>

  /** Open a workflow in the editor. */
  openWorkflow: (workflow: Workflow) => void
  /** Update the in-memory editor state (marks dirty). */
  setDefinition: (definition: Record<string, unknown>) => void
  /** Save the current editor state to the daemon. */
  save: () => Promise<void>
  /** Close the editor. */
  closeWorkflow: () => void
}

export const useWorkflowStore = create<WorkflowState>((set, get) => ({
  workflows: [],
  loading: false,
  current: null,
  dirty: false,
  saving: false,

  async fetchWorkflows() {
    set({ loading: true })
    try {
      const data = await api.get<WorkflowSummary[]>("/api/workflows", { silent: true })
      set({ workflows: Array.isArray(data) ? data : [], loading: false })
    } catch {
      set({ workflows: [], loading: false })
    }
  },

  async getWorkflow(id) {
    return api.get<Workflow>(`/api/workflows/${encodeURIComponent(id)}`)
  },

  async createWorkflow(req) {
    const created = await api.post<WorkflowSummary>("/api/workflows", req)
    await get().fetchWorkflows()
    return created
  },

  async updateWorkflow(id, req) {
    await api.put(`/api/workflows/${encodeURIComponent(id)}`, req)
    await get().fetchWorkflows()
  },

  async deleteWorkflow(id) {
    await api.delete(`/api/workflows/${encodeURIComponent(id)}`)
    set((s) => ({
      workflows: s.workflows.filter((w) => w.id !== id),
    }))
  },

  async duplicateWorkflow(id) {
    const dup = await api.post<WorkflowSummary>(
      `/api/workflows/${encodeURIComponent(id)}/duplicate`,
    )
    await get().fetchWorkflows()
    return dup
  },

  async validate(definition) {
    return api.post<ValidationResult>("/api/workflows/validate", definition)
  },

  async compile(definition) {
    return api.post<CompileResult>("/api/workflows/compile", definition)
  },

  openWorkflow(workflow) {
    set({ current: workflow, dirty: false })
  },

  setDefinition(definition) {
    const current = get().current
    if (!current) return
    set({
      current: { ...current, definition },
      dirty: true,
    })
  },

  async save() {
    const current = get().current
    if (!current) return

    set({ saving: true })
    try {
      await api.put(`/api/workflows/${encodeURIComponent(current.id)}`, {
        name: current.name,
        definition: current.definition,
        description: current.description,
        tags: current.tags,
      })
      set({ dirty: false, saving: false })
    } catch {
      set({ saving: false })
    }
  },

  closeWorkflow() {
    set({ current: null, dirty: false })
  },
}))
