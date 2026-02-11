import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { ScrollArea } from "@/components/ui/scroll-area"
import { cn } from "@/lib/utils"
import {
  useEditorStore,
  type AgentDef,
  type TaskDef,
  type ExecutionStrategy,
} from "@/stores/editor"

export function AgentTaskSidebar() {
  const agents = useEditorStore((s) => s.agents)
  const tasks = useEditorStore((s) => s.tasks)
  const strategy = useEditorStore((s) => s.strategy)
  const dependencies = useEditorStore((s) => s.dependencies)
  const selectedType = useEditorStore((s) => s.selectedType)
  const selectedId = useEditorStore((s) => s.selectedId)
  const addAgent = useEditorStore((s) => s.addAgent)
  const addTask = useEditorStore((s) => s.addTask)
  const removeAgent = useEditorStore((s) => s.removeAgent)
  const removeTask = useEditorStore((s) => s.removeTask)
  const setStrategy = useEditorStore((s) => s.setStrategy)
  const setDependencies = useEditorStore((s) => s.setDependencies)
  const select = useEditorStore((s) => s.select)

  return (
    <ScrollArea className="h-full">
      <div className="w-64 space-y-4 p-3">
        {/* Agents */}
        <section>
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-xs font-semibold uppercase text-muted-foreground tracking-wider">
              Agents
            </h3>
            <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={addAgent}>
              + Add
            </Button>
          </div>
          <div className="space-y-1">
            {agents.map((agent: AgentDef) => (
              <AgentCard
                key={agent.id}
                agent={agent}
                selected={selectedType === "agent" && selectedId === agent.id}
                onSelect={() => select("agent", agent.id)}
                onRemove={() => removeAgent(agent.id)}
              />
            ))}
            {agents.length === 0 && (
              <p className="text-[11px] text-muted-foreground px-1">
                No agents yet.
              </p>
            )}
          </div>
        </section>

        <Separator />

        {/* Tasks */}
        <section>
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-xs font-semibold uppercase text-muted-foreground tracking-wider">
              Tasks
            </h3>
            <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={addTask}>
              + Add
            </Button>
          </div>
          <div className="space-y-1">
            {tasks.map((task: TaskDef) => (
              <TaskCard
                key={task.id}
                task={task}
                agents={agents}
                selected={selectedType === "task" && selectedId === task.id}
                onSelect={() => select("task", task.id)}
                onRemove={() => removeTask(task.id)}
              />
            ))}
            {tasks.length === 0 && (
              <p className="text-[11px] text-muted-foreground px-1">
                No tasks yet.
              </p>
            )}
          </div>
        </section>

        <Separator />

        {/* Execution */}
        <section>
          <h3 className="text-xs font-semibold uppercase text-muted-foreground tracking-wider mb-2">
            Execution
          </h3>
          <div className="space-y-2">
            <Select
              value={strategy}
              onValueChange={(v) => setStrategy(v as ExecutionStrategy)}
            >
              <SelectTrigger className="h-8 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="sequential">Sequential</SelectItem>
                <SelectItem value="parallel">Parallel</SelectItem>
                <SelectItem value="hierarchical">Hierarchical</SelectItem>
                <SelectItem value="custom">Custom</SelectItem>
              </SelectContent>
            </Select>

            {strategy === "custom" && tasks.length > 0 && (
              <div className="space-y-2">
                <p className="text-[11px] text-muted-foreground">
                  Set dependencies for each task:
                </p>
                {tasks.map((task) => (
                  <div key={task.id} className="space-y-1">
                    <p className="text-[11px] font-medium">{task.id || "Unnamed"}</p>
                    <div className="flex flex-wrap gap-1">
                      {tasks
                        .filter((t) => t.id !== task.id)
                        .map((dep) => {
                          const isDependent = (dependencies[task.id] ?? []).includes(dep.id)
                          return (
                            <button
                              key={dep.id}
                              type="button"
                              className={cn(
                                "rounded px-1.5 py-0.5 text-[10px] border transition-colors",
                                isDependent
                                  ? "border-primary bg-primary/10 text-primary"
                                  : "border-border text-muted-foreground hover:border-primary/50",
                              )}
                              onClick={() => {
                                const current = dependencies[task.id] ?? []
                                setDependencies(
                                  task.id,
                                  isDependent
                                    ? current.filter((d) => d !== dep.id)
                                    : [...current, dep.id],
                                )
                              }}
                            >
                              {dep.id || "Unnamed"}
                            </button>
                          )
                        })}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </section>
      </div>
    </ScrollArea>
  )
}

function AgentCard({
  agent,
  selected,
  onSelect,
  onRemove,
}: {
  agent: AgentDef
  selected: boolean
  onSelect: () => void
  onRemove: () => void
}) {
  return (
    <div
      className={cn(
        "group flex items-center justify-between rounded-md border px-2 py-1.5 text-xs cursor-pointer transition-colors",
        selected
          ? "border-primary bg-primary/5"
          : "hover:border-primary/30 hover:bg-muted/50",
      )}
      onClick={onSelect}
    >
      <div className="min-w-0">
        <p className="font-medium truncate">
          {agent.role || agent.id}
        </p>
        {agent.model && (
          <p className="text-[10px] text-muted-foreground truncate">
            {agent.model}
          </p>
        )}
      </div>
      <button
        type="button"
        className="text-muted-foreground hover:text-destructive opacity-0 group-hover:opacity-100 transition-opacity ml-1 shrink-0"
        onClick={(e) => {
          e.stopPropagation()
          onRemove()
        }}
      >
        &times;
      </button>
    </div>
  )
}

function TaskCard({
  task,
  agents,
  selected,
  onSelect,
  onRemove,
}: {
  task: TaskDef
  agents: AgentDef[]
  selected: boolean
  onSelect: () => void
  onRemove: () => void
}) {
  const agent = agents.find((a) => a.id === task.agent)
  return (
    <div
      className={cn(
        "group flex items-center justify-between rounded-md border px-2 py-1.5 text-xs cursor-pointer transition-colors",
        selected
          ? "border-primary bg-primary/5"
          : "hover:border-primary/30 hover:bg-muted/50",
      )}
      onClick={onSelect}
    >
      <div className="min-w-0">
        <p className="font-medium truncate">
          {task.id}
        </p>
        {agent && (
          <Badge variant="secondary" className="text-[9px] mt-0.5">
            {agent.role || agent.id}
          </Badge>
        )}
      </div>
      <button
        type="button"
        className="text-muted-foreground hover:text-destructive opacity-0 group-hover:opacity-100 transition-opacity ml-1 shrink-0"
        onClick={(e) => {
          e.stopPropagation()
          onRemove()
        }}
      >
        &times;
      </button>
    </div>
  )
}
