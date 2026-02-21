import { useMemo } from 'react'
import { useWorkflowStore } from '@/stores/workflow'
import { useUIStore } from '@/stores/ui'
import { useProviderStore } from '@/stores/provider'
import { Icon } from '@/components/ui/icon'
import { FormInput, SliderInput } from './form-input'
import { cn } from '@/lib/utils'
import type { Agent, Task, ExecutionConfig, AgentWorkflow } from '@/lib/api-types'

export function AgentDesigner() {
  const activeSource = useWorkflowStore((s) => s.activeSource)
  const setActiveSource = useWorkflowStore((s) => s.setActiveSource)
  const designerTab = useUIStore((s) => s.designerTab)
  const setDesignerTab = useUIStore((s) => s.setDesignerTab)
  const selectedAgentId = useUIStore((s) => s.selectedAgentId)
  const selectAgent = useUIStore((s) => s.selectAgent)
  const selectedTaskId = useUIStore((s) => s.selectedTaskId)
  const selectTask = useUIStore((s) => s.selectTask)

  // Parse the source into a structured workflow
  const workflowData = useMemo((): AgentWorkflow | null => {
    if (!activeSource) return null
    try {
      // Handle both string (needs parsing) and object (already parsed) formats
      // The backend sends source as json.RawMessage which becomes an object when parsed
      if (typeof activeSource === 'string') {
        return JSON.parse(activeSource) as AgentWorkflow
      } else if (typeof activeSource === 'object') {
        return activeSource as unknown as AgentWorkflow
      }
      return null
    } catch {
      // Could be YAML - for now return null, full YAML support in later phase
      return null
    }
  }, [activeSource])

  const agents = useMemo(() => {
    if (!workflowData?.agents) return []
    return Object.entries(workflowData.agents).map(([id, agent]) => ({
      ...agent,
      id,
    }))
  }, [workflowData])

  const tasks = useMemo(() => {
    if (!workflowData?.tasks) return []
    return Object.entries(workflowData.tasks).map(([id, task]) => ({
      ...task,
      id,
    }))
  }, [workflowData])

  const selectedAgent = agents.find((a) => a.id === selectedAgentId)
  const selectedTask = tasks.find((t) => t.id === selectedTaskId)

  // Update functions
  const updateAgent = (agentId: string, updates: Partial<Agent>) => {
    if (!workflowData) return
    const updated = {
      ...workflowData,
      agents: {
        ...workflowData.agents,
        [agentId]: { ...workflowData.agents[agentId], ...updates },
      },
    }
    setActiveSource(JSON.stringify(updated, null, 2))
  }

  const updateTask = (taskId: string, updates: Partial<Task>) => {
    if (!workflowData) return
    const updated = {
      ...workflowData,
      tasks: {
        ...workflowData.tasks,
        [taskId]: { ...workflowData.tasks[taskId], ...updates },
      },
    }
    setActiveSource(JSON.stringify(updated, null, 2))
  }

  const updateExecution = (updates: Partial<ExecutionConfig>) => {
    if (!workflowData) return
    const updated = {
      ...workflowData,
      execution: { ...workflowData.execution, ...updates },
    }
    setActiveSource(JSON.stringify(updated, null, 2))
  }

  const addAgent = () => {
    if (!workflowData) return
    const id = `agent_${Date.now()}`
    const updated = {
      ...workflowData,
      agents: {
        ...workflowData.agents,
        [id]: {
          id,
          role: 'New Agent',
          goal: '',
          provider: '',
          model: '',
        },
      },
    }
    setActiveSource(JSON.stringify(updated, null, 2))
    selectAgent(id)
  }

  const addTask = () => {
    if (!workflowData) return
    const id = `task_${Date.now()}`
    const updated = {
      ...workflowData,
      tasks: {
        ...workflowData.tasks,
        [id]: {
          id,
          description: 'New task description',
          agent: agents[0]?.id || '',
        },
      },
    }
    setActiveSource(JSON.stringify(updated, null, 2))
    selectTask(id)
  }

  return (
    <div className="flex h-full overflow-hidden">
      {/* Left panel: form editors */}
      <div className="flex-[0_0_380px] border-r border-border overflow-auto bg-surface-0">
        {/* Tabs */}
        <div className="flex border-b border-border">
          {(['agents', 'tasks', 'execution'] as const).map((tab) => (
            <button
              key={tab}
              onClick={() => setDesignerTab(tab)}
              className={cn(
                'flex-1 py-3 border-b-2 text-xs font-semibold capitalize transition-colors',
                designerTab === tab
                  ? 'text-foreground border-primary'
                  : 'text-muted-foreground border-transparent hover:text-foreground'
              )}
            >
              {tab}
            </button>
          ))}
        </div>

        {/* Panel content */}
        <div className="p-[18px]">
          {designerTab === 'agents' && (
            <AgentsPanel
              agents={agents}
              selectedId={selectedAgentId}
              onSelect={selectAgent}
              onAdd={addAgent}
              selectedAgent={selectedAgent}
              onUpdate={updateAgent}
            />
          )}
          {designerTab === 'tasks' && (
            <TasksPanel
              tasks={tasks}
              agents={agents}
              selectedId={selectedTaskId}
              onSelect={selectTask}
              onAdd={addTask}
              selectedTask={selectedTask}
              onUpdate={updateTask}
            />
          )}
          {designerTab === 'execution' && (
            <ExecutionPanel
              execution={workflowData?.execution}
              tasks={tasks}
              onUpdate={updateExecution}
            />
          )}
        </div>
      </div>

      {/* Right panel: task graph preview */}
      <div className="flex-1 flex flex-col bg-surface-1">
        <div className="px-4 py-3 border-b border-border bg-surface-0">
          <span className="text-xs text-muted-foreground uppercase tracking-wide font-semibold">
            Task Graph
          </span>
        </div>
        <TaskGraphPreview tasks={tasks} />
      </div>
    </div>
  )
}

// Agents Panel
interface AgentsPanelProps {
  agents: (Agent & { id: string })[]
  selectedId: string | null
  onSelect: (id: string | null) => void
  onAdd: () => void
  selectedAgent?: Agent & { id: string }
  onUpdate: (id: string, updates: Partial<Agent>) => void
}

function AgentsPanel({
  agents,
  selectedId,
  onSelect,
  onAdd,
  selectedAgent,
  onUpdate,
}: AgentsPanelProps) {
  const providers = useProviderStore((s) => s.providers)

  const providerOptions = providers.map((p) => ({ value: p.id, label: p.name }))
  const selectedProvider = providers.find((p) => p.id === selectedAgent?.provider)

  return (
    <>
      <div className="flex justify-between items-center mb-4">
        <span className="text-[13px] font-bold text-foreground">
          Agents ({agents.length})
        </span>
        <Button variant="ghost" size="sm" icon="plus" onClick={onAdd}>
          Add
        </Button>
      </div>

      {/* Agent cards */}
      {agents.map((agent) => (
        <div
          key={agent.id}
          onClick={() => onSelect(agent.id)}
          className={cn(
            'p-3.5 rounded-[10px] mb-2.5 cursor-pointer transition-colors',
            selectedId === agent.id
              ? 'bg-accent-soft border border-primary'
              : 'bg-surface-1 border border-border hover:border-primary/50'
          )}
        >
          <div className="flex items-center gap-2 mb-1.5">
            <Icon
              name="bot"
              size={15}
              className={selectedId === agent.id ? 'text-primary' : 'text-muted-foreground'}
            />
            <span className="font-bold text-[13px] text-foreground">{agent.id}</span>
          </div>
          <div className="text-[11px] text-muted-foreground">{agent.role}</div>
        </div>
      ))}

      {/* Selected agent form */}
      {selectedAgent && (
        <div className="mt-5 pt-[18px] border-t border-border">
          <FormInput
            label="Agent ID"
            value={selectedAgent.id}
            hint="Unique identifier for this agent"
            disabled
          />
          <FormInput
            label="Role"
            value={selectedAgent.role}
            onChange={(v) => onUpdate(selectedAgent.id, { role: v })}
          />
          <FormInput
            label="Goal"
            value={selectedAgent.goal}
            onChange={(v) => onUpdate(selectedAgent.id, { goal: v })}
            type="textarea"
          />
          <FormInput
            label="Backstory"
            value={selectedAgent.backstory || ''}
            onChange={(v) => onUpdate(selectedAgent.id, { backstory: v })}
            type="textarea"
            hint="Optional — gives the LLM persona context"
          />
          <div className="grid grid-cols-2 gap-2.5">
            <FormInput
              label="Provider"
              value={selectedAgent.provider}
              onChange={(v) => onUpdate(selectedAgent.id, { provider: v, model: '' })}
              type="select"
              options={providerOptions}
              placeholder="Select provider"
            />
            <FormInput
              label="Model"
              value={selectedAgent.model}
              onChange={(v) => onUpdate(selectedAgent.id, { model: v })}
              type="select"
              options={
                selectedProvider
                  ? [{ value: selectedProvider.default_model || '', label: selectedProvider.default_model || 'Default' }]
                  : []
              }
              placeholder="Select model"
            />
          </div>
          <FormInput
            label="Tools"
            value={selectedAgent.tools?.join(', ') || ''}
            onChange={(v) =>
              onUpdate(selectedAgent.id, {
                tools: v
                  .split(',')
                  .map((t) => t.trim())
                  .filter(Boolean),
              })
            }
            hint="Comma-separated tool.action references"
          />
          <div className="grid grid-cols-2 gap-2.5">
            <SliderInput
              label="Temperature"
              value={selectedAgent.temperature ?? 0.7}
              onChange={(v) => onUpdate(selectedAgent.id, { temperature: v })}
              min={0}
              max={2}
              step={0.1}
            />
            <FormInput
              label="Max Tokens"
              value={String(selectedAgent.max_tokens || 4096)}
              onChange={(v) => onUpdate(selectedAgent.id, { max_tokens: parseInt(v) || 4096 })}
              type="number"
            />
          </div>
        </div>
      )}
    </>
  )
}

// Tasks Panel
interface TasksPanelProps {
  tasks: (Task & { id: string })[]
  agents: (Agent & { id: string })[]
  selectedId: string | null
  onSelect: (id: string | null) => void
  onAdd: () => void
  selectedTask?: Task & { id: string }
  onUpdate: (id: string, updates: Partial<Task>) => void
}

function TasksPanel({
  tasks,
  agents,
  selectedId,
  onSelect,
  onAdd,
  selectedTask,
  onUpdate,
}: TasksPanelProps) {
  const agentOptions = agents.map((a) => ({ value: a.id, label: a.id }))

  return (
    <>
      <div className="flex justify-between items-center mb-4">
        <span className="text-[13px] font-bold text-foreground">
          Tasks ({tasks.length})
        </span>
        <Button variant="ghost" size="sm" icon="plus" onClick={onAdd}>
          Add
        </Button>
      </div>

      {/* Task cards */}
      {tasks.map((task) => (
        <div
          key={task.id}
          onClick={() => onSelect(task.id)}
          className={cn(
            'p-3.5 rounded-[10px] mb-2.5 cursor-pointer transition-colors',
            selectedId === task.id
              ? 'bg-accent-soft border border-primary'
              : 'bg-surface-1 border border-border hover:border-primary/50'
          )}
        >
          <div className="flex items-center gap-2 mb-1">
            <Icon
              name="list"
              size={14}
              className={selectedId === task.id ? 'text-primary' : 'text-muted-foreground'}
            />
            <span className="font-semibold text-[13px] text-foreground">{task.id}</span>
          </div>
          <div className="text-[11px] text-muted-foreground">→ {task.agent}</div>
        </div>
      ))}

      {/* Selected task form */}
      {selectedTask && (
        <div className="mt-5 pt-[18px] border-t border-border">
          <FormInput
            label="Task ID"
            value={selectedTask.id}
            disabled
          />
          <FormInput
            label="Description"
            value={selectedTask.description}
            onChange={(v) => onUpdate(selectedTask.id, { description: v })}
            type="textarea"
            hint="This becomes the prompt sent to the assigned agent"
          />
          <FormInput
            label="Assigned Agent"
            value={selectedTask.agent}
            onChange={(v) => onUpdate(selectedTask.id, { agent: v })}
            type="select"
            options={agentOptions}
            placeholder="Select agent"
          />
          <FormInput
            label="Expected Output"
            value={selectedTask.expected_output || ''}
            onChange={(v) => onUpdate(selectedTask.id, { expected_output: v })}
            type="textarea"
          />
          <FormInput
            label="Output Key"
            value={selectedTask.output_key || ''}
            onChange={(v) => onUpdate(selectedTask.id, { output_key: v })}
            hint="Downstream tasks reference this as input"
          />
          <FormInput
            label="Depends On"
            value={selectedTask.depends_on?.join(', ') || ''}
            onChange={(v) =>
              onUpdate(selectedTask.id, {
                depends_on: v
                  .split(',')
                  .map((t) => t.trim())
                  .filter(Boolean),
              })
            }
            hint="Comma-separated task IDs (for custom strategy)"
          />
        </div>
      )}
    </>
  )
}

// Execution Panel
interface ExecutionPanelProps {
  execution?: ExecutionConfig
  tasks: (Task & { id: string })[]
  onUpdate: (updates: Partial<ExecutionConfig>) => void
}

function ExecutionPanel({ execution, tasks, onUpdate }: ExecutionPanelProps) {
  const strategies: ExecutionConfig['strategy'][] = [
    'sequential',
    'parallel',
    'hierarchical',
    'custom',
  ]

  const strategyDescriptions: Record<ExecutionConfig['strategy'], string> = {
    sequential: 'Tasks run one after another in defined order',
    parallel: 'All tasks run concurrently',
    hierarchical: 'Manager agent coordinates task execution',
    custom: 'Tasks run based on depends_on relationships',
  }

  return (
    <div>
      <div className="text-[13px] font-bold text-foreground mb-3.5">
        Execution Strategy
      </div>
      {strategies.map((strategy) => (
        <label
          key={strategy}
          className={cn(
            'flex items-start gap-3 p-3 rounded-lg mb-2 cursor-pointer transition-colors',
            execution?.strategy === strategy
              ? 'bg-accent-soft border border-primary'
              : 'bg-surface-1 border border-border hover:border-primary/50'
          )}
        >
          <input
            type="radio"
            name="strategy"
            value={strategy}
            checked={execution?.strategy === strategy}
            onChange={() => onUpdate({ strategy })}
            className="mt-0.5 accent-primary"
          />
          <div>
            <div className="font-semibold text-[13px] text-foreground capitalize">
              {strategy}
            </div>
            <div className="text-[11px] text-muted-foreground">
              {strategyDescriptions[strategy]}
            </div>
          </div>
        </label>
      ))}

      {/* Task order for sequential */}
      {execution?.strategy === 'sequential' && tasks.length > 0 && (
        <div className="mt-5 pt-4 border-t border-border">
          <div className="text-xs font-semibold text-muted-foreground mb-3">
            Task Order
          </div>
          {(execution.task_order || tasks.map((t) => t.id)).map((taskId, idx) => (
            <div
              key={taskId}
              className="flex items-center gap-2 p-2 bg-surface-1 rounded-lg mb-2"
            >
              <span className="text-xs text-muted-foreground w-5">{idx + 1}.</span>
              <span className="text-sm text-foreground">{taskId}</span>
            </div>
          ))}
          <p className="text-[11px] text-muted-foreground mt-2">
            Drag to reorder (coming soon)
          </p>
        </div>
      )}
    </div>
  )
}

// Task Graph Preview
function TaskGraphPreview({ tasks }: { tasks: (Task & { id: string })[] }) {
  if (tasks.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-muted-foreground text-sm p-4 text-center">
        <div>
          <Icon name="git" size={32} className="mx-auto mb-2 opacity-50" />
          <p>Add tasks to see the dependency graph</p>
        </div>
      </div>
    )
  }

  // Simple text-based graph preview until React Flow is integrated
  return (
    <div className="flex-1 p-4 overflow-auto">
      <div className="text-xs text-muted-foreground mb-3">
        Task Dependencies (React Flow in Phase 6)
      </div>
      <div className="space-y-2">
        {tasks.map((task) => (
          <div
            key={task.id}
            className="flex items-center gap-2 p-3 bg-surface-0 rounded-lg border border-border"
          >
            <div className="w-3 h-3 rounded-full bg-primary" />
            <div className="flex-1">
              <div className="text-sm font-medium text-foreground">{task.id}</div>
              {task.depends_on && task.depends_on.length > 0 && (
                <div className="text-[11px] text-muted-foreground">
                  ← depends on: {task.depends_on.join(', ')}
                </div>
              )}
            </div>
            <div className="text-xs text-muted-foreground">→ {task.agent}</div>
          </div>
        ))}
      </div>
    </div>
  )
}

// Button component
interface ButtonProps {
  children: React.ReactNode
  variant?: 'primary' | 'secondary' | 'ghost'
  size?: 'sm' | 'md'
  icon?: 'plus' | 'play'
  onClick?: () => void
  disabled?: boolean
}

function Button({
  children,
  variant = 'secondary',
  size = 'md',
  icon,
  onClick,
  disabled,
}: ButtonProps) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-lg font-semibold transition-all',
        size === 'sm' ? 'text-xs px-2.5 py-1.5' : 'text-[13px] px-3.5 py-2',
        disabled && 'opacity-50 cursor-not-allowed',
        variant === 'primary' && 'bg-primary text-white hover:bg-primary/90',
        variant === 'secondary' &&
          'bg-surface-2 text-foreground border border-border hover:bg-surface-active',
        variant === 'ghost' && 'bg-transparent text-muted-foreground hover:text-foreground'
      )}
    >
      {icon && <Icon name={icon} size={size === 'sm' ? 13 : 15} />}
      {children}
    </button>
  )
}
