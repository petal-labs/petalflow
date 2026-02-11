import { ScrollArea } from "@/components/ui/scroll-area"
import { AgentForm } from "@/components/designer/agent-form"
import { TaskForm } from "@/components/designer/task-form"
import { useEditorStore } from "@/stores/editor"

interface DetailPanelProps {
  onRegisterTool?: () => void
}

export function DetailPanel({ onRegisterTool }: DetailPanelProps) {
  const selectedType = useEditorStore((s) => s.selectedType)
  const selectedId = useEditorStore((s) => s.selectedId)
  const agents = useEditorStore((s) => s.agents)
  const tasks = useEditorStore((s) => s.tasks)

  if (!selectedType || !selectedId) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Select an agent or task to edit
      </div>
    )
  }

  if (selectedType === "agent") {
    const agent = agents.find((a) => a.id === selectedId)
    if (!agent) return null
    return (
      <ScrollArea className="h-full">
        <AgentForm agent={agent} onRegisterTool={onRegisterTool} />
      </ScrollArea>
    )
  }

  if (selectedType === "task") {
    const task = tasks.find((t) => t.id === selectedId)
    if (!task) return null
    return (
      <ScrollArea className="h-full">
        <TaskForm task={task} />
      </ScrollArea>
    )
  }

  return null
}
