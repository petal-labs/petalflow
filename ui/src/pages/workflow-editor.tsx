import { useCallback, useEffect, useState } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { ReactFlowProvider } from "@xyflow/react"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { DesignerToolbar } from "@/components/designer/designer-toolbar"
import { AgentTaskSidebar } from "@/components/designer/agent-task-sidebar"
import { DetailPanel } from "@/components/designer/detail-panel"
import { GraphPreview } from "@/components/designer/graph-preview"
import { GraphCanvas } from "@/components/designer/graph-canvas"
import { NodePalette } from "@/components/designer/node-palette"
import { NodeInspector } from "@/components/designer/node-inspector"
import { EdgeInspector } from "@/components/designer/edge-inspector"
import { EjectDialog } from "@/components/designer/eject-dialog"
import { IssuesPanel } from "@/components/designer/issues-panel"
import { SourceTab } from "@/components/designer/source-tab"
import { WorkflowSettingsPanel } from "@/components/designer/workflow-settings-panel"
import { RegisterToolSheet } from "@/components/tools/register-tool-sheet"
import { RunModal } from "@/components/runner/run-modal"
import { useWorkflowStore } from "@/stores/workflows"
import { useEditorStore } from "@/stores/editor"
import { useGraphStore } from "@/stores/graph"
import { useAutoSave } from "@/hooks/use-auto-save"
import { toast } from "sonner"

export default function WorkflowEditorPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const getWorkflow = useWorkflowStore((s) => s.getWorkflow)
  const openWorkflow = useWorkflowStore((s) => s.openWorkflow)
  const current = useWorkflowStore((s) => s.current)
  const dirty = useWorkflowStore((s) => s.dirty)
  const saving = useWorkflowStore((s) => s.saving)
  const setDefinition = useWorkflowStore((s) => s.setDefinition)
  const closeWorkflow = useWorkflowStore((s) => s.closeWorkflow)

  const loadDefinition = useEditorStore((s) => s.loadDefinition)
  const toDefinition = useEditorStore((s) => s.toDefinition)
  const reset = useEditorStore((s) => s.reset)
  const agents = useEditorStore((s) => s.agents)
  const tasks = useEditorStore((s) => s.tasks)
  const strategy = useEditorStore((s) => s.strategy)
  const dependencies = useEditorStore((s) => s.dependencies)

  const loadFromGraphIR = useGraphStore((s) => s.loadFromGraphIR)
  const toGraphIR = useGraphStore((s) => s.toGraphIR)
  const graphNodes = useGraphStore((s) => s.nodes)
  const graphEdges = useGraphStore((s) => s.edges)
  const resetGraph = useGraphStore((s) => s.reset)
  const selectedNodeId = useGraphStore((s) => s.selectedNodeId)
  const selectedEdgeId = useGraphStore((s) => s.selectedEdgeId)

  const compile = useWorkflowStore((s) => s.compile)

  const [settingsOpen, setSettingsOpen] = useState(false)
  const [registerToolOpen, setRegisterToolOpen] = useState(false)
  const [ejectOpen, setEjectOpen] = useState(false)
  const [runModalOpen, setRunModalOpen] = useState(false)
  const [paletteCollapsed, setPaletteCollapsed] = useState(false)
  const [inspectorCollapsed, setInspectorCollapsed] = useState(false)

  useAutoSave()

  // Load workflow on mount
  useEffect(() => {
    if (!id) return
    let cancelled = false
    ;(async () => {
      try {
        const wf = await getWorkflow(id)
        if (cancelled) return
        openWorkflow(wf)
        if (wf.kind === "agent_workflow" && wf.definition) {
          loadDefinition(wf.definition)
        } else if (wf.kind === "graph" && wf.definition) {
          loadFromGraphIR(wf.definition)
        }
      } catch {
        toast.error("Failed to load workflow.")
      }
    })()
    return () => {
      cancelled = true
      closeWorkflow()
      reset()
      resetGraph()
    }
  }, [id, getWorkflow, openWorkflow, closeWorkflow, loadDefinition, loadFromGraphIR, reset, resetGraph])

  // Sync editor state → workflow definition (for save)
  useEffect(() => {
    if (!current || current.kind !== "agent_workflow") return
    const def = toDefinition()
    setDefinition(def)
  }, [agents, tasks, strategy, dependencies, current, toDefinition, setDefinition])

  // Sync graph state → workflow definition (for save)
  useEffect(() => {
    if (!current || current.kind !== "graph") return
    const ir = toGraphIR()
    setDefinition(ir)
  }, [graphNodes, graphEdges, current, toGraphIR, setDefinition])

  // Eject Agent/Task → Graph
  const handleEject = useCallback(async () => {
    if (!current) return
    try {
      const def = toDefinition()
      const result = await compile(def)
      // Switch workflow kind to graph
      openWorkflow({ ...current, kind: "graph" })
      loadFromGraphIR(result.graph)
      reset() // clear agent/task editor state
      toast.success("Ejected to graph mode.")
    } catch {
      toast.error("Eject failed — could not compile workflow.")
    }
  }, [current, toDefinition, compile, openWorkflow, loadFromGraphIR, reset])

  if (!current) {
    return (
      <div className="flex h-[calc(100vh-3.5rem)] items-center justify-center text-sm text-muted-foreground">
        Loading...
      </div>
    )
  }

  const isAgentMode = current.kind === "agent_workflow"

  return (
    <ReactFlowProvider>
      <div className="flex h-[calc(100vh-3.5rem)] flex-col">
        {/* Toolbar */}
        <DesignerToolbar
          workflowName={current.name}
          kind={current.kind}
          saving={saving}
          dirty={dirty}
          onRun={() => setRunModalOpen(true)}
          onSettings={() => setSettingsOpen(true)}
          onEject={isAgentMode ? () => setEjectOpen(true) : undefined}
        />

        {/* Main content */}
        <div className="flex flex-1 overflow-hidden">
          {/* Sidebar (Agent/Task mode only) */}
          {isAgentMode && (
            <div className="border-r shrink-0">
              <AgentTaskSidebar />
            </div>
          )}

          {/* Center area */}
          <div className="flex flex-1 flex-col overflow-hidden">
            <Tabs defaultValue="canvas" className="flex flex-1 flex-col overflow-hidden">
              <TabsList className="mx-3 mt-2 w-fit">
                <TabsTrigger value="canvas">Canvas</TabsTrigger>
                <TabsTrigger value="source">Source</TabsTrigger>
              </TabsList>

              <TabsContent value="canvas" className="flex-1 overflow-hidden m-0">
                {isAgentMode ? (
                  <div className="flex h-full">
                    <div className="flex-1 border-r">
                      <GraphPreview />
                    </div>
                    <div className="w-80 shrink-0">
                      <DetailPanel
                        onRegisterTool={() => setRegisterToolOpen(true)}
                      />
                    </div>
                  </div>
                ) : (
                  <div className="flex h-full">
                    {/* Node Palette (collapsible) */}
                    {!paletteCollapsed && (
                      <div className="w-52 shrink-0 border-r overflow-y-auto">
                        <NodePalette />
                      </div>
                    )}
                    <button
                      type="button"
                      className="shrink-0 w-5 flex items-center justify-center border-r text-muted-foreground hover:bg-muted/50 transition-colors text-[10px]"
                      onClick={() => setPaletteCollapsed(!paletteCollapsed)}
                      title={paletteCollapsed ? "Show palette" : "Hide palette"}
                    >
                      {paletteCollapsed ? "\u203A" : "\u2039"}
                    </button>
                    {/* Canvas */}
                    <div className="flex-1">
                      <GraphCanvas />
                    </div>
                    {/* Inspector (collapsible) */}
                    <button
                      type="button"
                      className="shrink-0 w-5 flex items-center justify-center border-l text-muted-foreground hover:bg-muted/50 transition-colors text-[10px]"
                      onClick={() => setInspectorCollapsed(!inspectorCollapsed)}
                      title={inspectorCollapsed ? "Show inspector" : "Hide inspector"}
                    >
                      {inspectorCollapsed ? "\u2039" : "\u203A"}
                    </button>
                    {!inspectorCollapsed && (
                      <div className="w-80 shrink-0 border-l overflow-y-auto">
                        {selectedNodeId ? (
                          <NodeInspector nodeId={selectedNodeId} />
                        ) : selectedEdgeId ? (
                          <EdgeInspector edgeId={selectedEdgeId} />
                        ) : (
                          <div className="flex h-full items-center justify-center p-4 text-xs text-muted-foreground">
                            Select a node or edge to inspect
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                )}
              </TabsContent>

              <TabsContent value="source" className="flex-1 overflow-hidden m-0">
                <SourceTab mode={current.kind} />
              </TabsContent>
            </Tabs>

            {/* Issues panel */}
            <IssuesPanel mode={current.kind} />
          </div>
        </div>

        {/* Workflow settings sheet */}
        <WorkflowSettingsPanel
          workflow={current}
          open={settingsOpen}
          onOpenChange={setSettingsOpen}
          onUpdate={(patch) => {
            if (patch.name !== undefined || patch.description !== undefined || patch.tags !== undefined) {
              openWorkflow({ ...current, ...patch })
            }
          }}
        />

        {/* Tool registration sheet */}
        <RegisterToolSheet
          open={registerToolOpen}
          onOpenChange={setRegisterToolOpen}
        />

        {/* Eject dialog */}
        <EjectDialog
          open={ejectOpen}
          onOpenChange={setEjectOpen}
          onConfirm={handleEject}
        />

        {/* Run modal */}
        <RunModal
          open={runModalOpen}
          onOpenChange={setRunModalOpen}
          workflow={current}
          onStarted={(runId) => navigate(`/runs/${runId}`)}
        />
      </div>
    </ReactFlowProvider>
  )
}
