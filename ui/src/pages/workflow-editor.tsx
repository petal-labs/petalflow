import { useEffect, useState } from "react"
import { useParams } from "react-router-dom"
import { ReactFlowProvider } from "@xyflow/react"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { DesignerToolbar } from "@/components/designer/designer-toolbar"
import { AgentTaskSidebar } from "@/components/designer/agent-task-sidebar"
import { DetailPanel } from "@/components/designer/detail-panel"
import { GraphPreview } from "@/components/designer/graph-preview"
import { IssuesPanel } from "@/components/designer/issues-panel"
import { SourceTab } from "@/components/designer/source-tab"
import { WorkflowSettingsPanel } from "@/components/designer/workflow-settings-panel"
import { RegisterToolSheet } from "@/components/tools/register-tool-sheet"
import { useWorkflowStore } from "@/stores/workflows"
import { useEditorStore } from "@/stores/editor"
import { useAutoSave } from "@/hooks/use-auto-save"
import { toast } from "sonner"

export default function WorkflowEditorPage() {
  const { id } = useParams<{ id: string }>()
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

  const [settingsOpen, setSettingsOpen] = useState(false)
  const [registerToolOpen, setRegisterToolOpen] = useState(false)

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
        }
      } catch {
        toast.error("Failed to load workflow.")
      }
    })()
    return () => {
      cancelled = true
      closeWorkflow()
      reset()
    }
  }, [id, getWorkflow, openWorkflow, closeWorkflow, loadDefinition, reset])

  // Sync editor state → workflow definition (for save)
  useEffect(() => {
    if (!current || current.kind !== "agent_workflow") return
    const def = toDefinition()
    setDefinition(def)
  }, [agents, tasks, strategy, dependencies, current, toDefinition, setDefinition])

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
          onRun={() => toast.info("Run modal — coming soon.")}
          onSettings={() => setSettingsOpen(true)}
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
                  <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                    Graph mode canvas — coming in Phase 4
                  </div>
                )}
              </TabsContent>

              <TabsContent value="source" className="flex-1 overflow-hidden m-0">
                <SourceTab />
              </TabsContent>
            </Tabs>

            {/* Issues panel */}
            <IssuesPanel />
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
      </div>
    </ReactFlowProvider>
  )
}
