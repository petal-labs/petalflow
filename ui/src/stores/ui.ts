import { create } from 'zustand'
import type { NodeType } from '@/lib/api-types'
import { nodeTypesApi } from '@/lib/api-client'

export type DesignerMode = 'agent' | 'graph'
export type DesignerTab = 'agents' | 'tasks' | 'execution'

export interface UIState {
  // Designer state
  designerMode: DesignerMode
  designerTab: DesignerTab
  selectedAgentId: string | null
  selectedTaskId: string | null
  selectedNodeId: string | null

  // Run viewer state
  selectedEventIndex: number | null

  // Node types for palette
  nodeTypes: NodeType[]
  nodeTypesLoading: boolean

  // Modals
  runModalOpen: boolean
  createWorkflowModalOpen: boolean
}

export interface UIActions {
  // Designer actions
  setDesignerMode: (mode: DesignerMode) => void
  setDesignerTab: (tab: DesignerTab) => void
  selectAgent: (id: string | null) => void
  selectTask: (id: string | null) => void
  selectNode: (id: string | null) => void

  // Run viewer actions
  selectEvent: (index: number | null) => void

  // Node types
  fetchNodeTypes: () => Promise<void>

  // Modals
  openRunModal: () => void
  closeRunModal: () => void
  openCreateWorkflowModal: () => void
  closeCreateWorkflowModal: () => void

  // Reset
  resetDesignerState: () => void
}

const initialState: UIState = {
  designerMode: 'agent',
  designerTab: 'agents',
  selectedAgentId: null,
  selectedTaskId: null,
  selectedNodeId: null,
  selectedEventIndex: null,
  nodeTypes: [],
  nodeTypesLoading: false,
  runModalOpen: false,
  createWorkflowModalOpen: false,
}

export const useUIStore = create<UIState & UIActions>()((set) => ({
  ...initialState,

  setDesignerMode: (mode) => set({ designerMode: mode }),

  setDesignerTab: (tab) => set({ designerTab: tab }),

  selectAgent: (id) => set({ selectedAgentId: id, designerTab: 'agents' }),

  selectTask: (id) => set({ selectedTaskId: id, designerTab: 'tasks' }),

  selectNode: (id) => set({ selectedNodeId: id }),

  selectEvent: (index) => set({ selectedEventIndex: index }),

  fetchNodeTypes: async () => {
    set({ nodeTypesLoading: true })
    try {
      const nodeTypes = await nodeTypesApi.list()
      set({ nodeTypes, nodeTypesLoading: false })
    } catch (err) {
      console.error('Failed to fetch node types:', err)
      set({ nodeTypesLoading: false })
    }
  },

  openRunModal: () => set({ runModalOpen: true }),
  closeRunModal: () => set({ runModalOpen: false }),

  openCreateWorkflowModal: () => set({ createWorkflowModalOpen: true }),
  closeCreateWorkflowModal: () => set({ createWorkflowModalOpen: false }),

  resetDesignerState: () =>
    set({
      selectedAgentId: null,
      selectedTaskId: null,
      selectedNodeId: null,
      designerTab: 'agents',
    }),
}))

// Helper to get node types grouped by category
export function useNodeTypesByCategory() {
  const nodeTypes = useUIStore((s) => s.nodeTypes)

  const grouped: Record<string, NodeType[]> = {}
  for (const nt of nodeTypes) {
    const category = nt.category || 'Other'
    if (!grouped[category]) {
      grouped[category] = []
    }
    grouped[category].push(nt)
  }

  return grouped
}
