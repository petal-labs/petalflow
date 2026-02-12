import { create } from "zustand"

// ---------------------------------------------------------------------------
// Agent/Task editor state (in-memory, separate from workflow store)
// ---------------------------------------------------------------------------

export interface AgentDef {
  id: string
  role: string
  goal: string
  backstory: string
  provider: string
  model: string
  tools: string[] // "toolName.actionName" format
  temperature?: number
  max_tokens?: number
  system_prompt?: string
}

export interface TaskDef {
  id: string
  description: string
  agent: string // agent id
  expected_output: string
  output_key: string
  inputs: Record<string, string>
  human_review: boolean
  review_instructions: string
}

export type ExecutionStrategy = "sequential" | "parallel" | "hierarchical" | "custom"

export interface EditorState {
  agents: AgentDef[]
  tasks: TaskDef[]
  strategy: ExecutionStrategy
  /** Per-task dependency list (task id -> depends-on task ids), used when strategy = "custom". */
  dependencies: Record<string, string[]>

  /** Currently selected item in the sidebar. */
  selectedType: "agent" | "task" | null
  selectedId: string | null

  // Actions
  addAgent: () => void
  updateAgent: (id: string, patch: Partial<AgentDef>) => void
  removeAgent: (id: string) => void
  reorderAgents: (fromIndex: number, toIndex: number) => void

  addTask: () => void
  updateTask: (id: string, patch: Partial<TaskDef>) => void
  removeTask: (id: string) => void

  setStrategy: (strategy: ExecutionStrategy) => void
  setDependencies: (taskId: string, deps: string[]) => void

  select: (type: "agent" | "task", id: string) => void
  clearSelection: () => void

  /** Build the agent_workflow definition object for save/compile. */
  toDefinition: () => Record<string, unknown>
  /** Load from a workflow definition. */
  loadDefinition: (def: Record<string, unknown>) => void
  /** Reset editor state. */
  reset: () => void
}

let agentCounter = 0
let taskCounter = 0

function makeAgentId() {
  agentCounter++
  return `agent_${agentCounter}`
}

function makeTaskId() {
  taskCounter++
  return `task_${taskCounter}`
}

export const useEditorStore = create<EditorState>((set, get) => ({
  agents: [],
  tasks: [],
  strategy: "sequential",
  dependencies: {},
  selectedType: null,
  selectedId: null,

  addAgent() {
    const id = makeAgentId()
    const agent: AgentDef = {
      id,
      role: "",
      goal: "",
      backstory: "",
      provider: "",
      model: "",
      tools: [],
    }
    set((s) => ({
      agents: [...s.agents, agent],
      selectedType: "agent",
      selectedId: id,
    }))
  },

  updateAgent(id, patch) {
    set((s) => {
      const idx = s.agents.findIndex((a) => a.id === id)
      if (idx === -1) return {}

      const requestedId = patch.id
      const canRename =
        typeof requestedId === "string" &&
        requestedId !== id &&
        !s.agents.some((a, i) => i !== idx && a.id === requestedId)
      const nextId = canRename ? requestedId : id

      const nextAgents = s.agents.map((a) =>
        a.id === id ? { ...a, ...patch, id: nextId } : a,
      )

      if (!canRename) {
        return { agents: nextAgents }
      }

      return {
        agents: nextAgents,
        tasks: s.tasks.map((t) =>
          t.agent === id ? { ...t, agent: nextId } : t,
        ),
        selectedId:
          s.selectedType === "agent" && s.selectedId === id
            ? nextId
            : s.selectedId,
      }
    })
  },

  removeAgent(id) {
    set((s) => ({
      agents: s.agents.filter((a) => a.id !== id),
      tasks: s.tasks.map((t) => (t.agent === id ? { ...t, agent: "" } : t)),
      selectedType: s.selectedId === id ? null : s.selectedType,
      selectedId: s.selectedId === id ? null : s.selectedId,
    }))
  },

  reorderAgents(fromIndex, toIndex) {
    set((s) => {
      const agents = [...s.agents]
      const [moved] = agents.splice(fromIndex, 1)
      agents.splice(toIndex, 0, moved)
      return { agents }
    })
  },

  addTask() {
    const id = makeTaskId()
    const task: TaskDef = {
      id,
      description: "",
      agent: "",
      expected_output: "",
      output_key: "",
      inputs: {},
      human_review: false,
      review_instructions: "",
    }
    set((s) => ({
      tasks: [...s.tasks, task],
      selectedType: "task",
      selectedId: id,
    }))
  },

  updateTask(id, patch) {
    set((s) => ({
      tasks: s.tasks.map((t) => (t.id === id ? { ...t, ...patch } : t)),
    }))
  },

  removeTask(id) {
    set((s) => {
      const deps = { ...s.dependencies }
      delete deps[id]
      // Remove from other tasks' deps
      for (const key of Object.keys(deps)) {
        deps[key] = deps[key].filter((d) => d !== id)
      }
      return {
        tasks: s.tasks.filter((t) => t.id !== id),
        dependencies: deps,
        selectedType: s.selectedId === id ? null : s.selectedType,
        selectedId: s.selectedId === id ? null : s.selectedId,
      }
    })
  },

  setStrategy(strategy) {
    set({ strategy })
  },

  setDependencies(taskId, deps) {
    set((s) => ({
      dependencies: { ...s.dependencies, [taskId]: deps },
    }))
  },

  select(type, id) {
    set({ selectedType: type, selectedId: id })
  },

  clearSelection() {
    set({ selectedType: null, selectedId: null })
  },

  toDefinition() {
    const { agents, tasks, strategy, dependencies } = get()
    return {
      agents: agents.map((a) => ({
        id: a.id,
        role: a.role,
        goal: a.goal,
        backstory: a.backstory || undefined,
        provider: a.provider,
        model: a.model,
        tools: a.tools.length > 0 ? a.tools : undefined,
        temperature: a.temperature,
        max_tokens: a.max_tokens,
        system_prompt: a.system_prompt || undefined,
      })),
      tasks: tasks.map((t) => ({
        id: t.id,
        description: t.description,
        agent: t.agent,
        expected_output: t.expected_output,
        output_key: t.output_key || undefined,
        inputs:
          Object.keys(t.inputs).length > 0 ? t.inputs : undefined,
        human_review: t.human_review || undefined,
        review_instructions: t.human_review
          ? t.review_instructions || undefined
          : undefined,
      })),
      execution: {
        strategy,
        dependencies:
          strategy === "custom"
            ? dependencies
            : undefined,
      },
    }
  },

  loadDefinition(def) {
    const agents: AgentDef[] = (
      (def.agents as Array<Record<string, unknown>>) ?? []
    ).map((a) => ({
      id: String(a.id ?? ""),
      role: String(a.role ?? ""),
      goal: String(a.goal ?? ""),
      backstory: String(a.backstory ?? ""),
      provider: String(a.provider ?? ""),
      model: String(a.model ?? ""),
      tools: (a.tools as string[]) ?? [],
      temperature: a.temperature as number | undefined,
      max_tokens: a.max_tokens as number | undefined,
      system_prompt: a.system_prompt as string | undefined,
    }))

    const tasks: TaskDef[] = (
      (def.tasks as Array<Record<string, unknown>>) ?? []
    ).map((t) => ({
      id: String(t.id ?? ""),
      description: String(t.description ?? ""),
      agent: String(t.agent ?? ""),
      expected_output: String(t.expected_output ?? ""),
      output_key: String(t.output_key ?? ""),
      inputs: (t.inputs as Record<string, string>) ?? {},
      human_review: Boolean(t.human_review),
      review_instructions: String(t.review_instructions ?? ""),
    }))

    const exec = def.execution as Record<string, unknown> | undefined
    const strategy = (exec?.strategy as ExecutionStrategy) ?? "sequential"
    const dependencies =
      (exec?.dependencies as Record<string, string[]>) ?? {}

    // Update counters so new IDs don't collide
    agentCounter = agents.length
    taskCounter = tasks.length

    set({
      agents,
      tasks,
      strategy,
      dependencies,
      selectedType: null,
      selectedId: null,
    })
  },

  reset() {
    agentCounter = 0
    taskCounter = 0
    set({
      agents: [],
      tasks: [],
      strategy: "sequential",
      dependencies: {},
      selectedType: null,
      selectedId: null,
    })
  },
}))
