import { useState } from "react"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Separator } from "@/components/ui/separator"
import { TemplateHelper } from "@/components/designer/template-helper"
import { useEditorStore, type TaskDef } from "@/stores/editor"

interface TaskFormProps {
  task: TaskDef
}

export function TaskForm({ task }: TaskFormProps) {
  const agents = useEditorStore((s) => s.agents)
  const updateTask = useEditorStore((s) => s.updateTask)
  const removeTask = useEditorStore((s) => s.removeTask)

  const update = (patch: Partial<TaskDef>) => updateTask(task.id, patch)

  return (
    <div className="space-y-4 p-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">Task</h3>
        <Button
          variant="ghost"
          size="sm"
          className="text-destructive hover:text-destructive h-6 text-xs"
          onClick={() => removeTask(task.id)}
        >
          Remove
        </Button>
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">ID</Label>
        <Input
          value={task.id}
          onChange={(e) => update({ id: e.target.value })}
          className="h-8 text-xs font-mono"
        />
      </div>

      <div className="space-y-1.5">
        <div className="flex items-center gap-2">
          <Label className="text-xs">
            Description <span className="text-destructive">*</span>
          </Label>
          <TemplateHelper />
        </div>
        <Textarea
          value={task.description}
          onChange={(e) => update({ description: e.target.value })}
          placeholder="What this task should accomplish. Use {{input.topic}} or {{tasks.other.output}} for variables."
          className="text-xs min-h-[80px]"
        />
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">
          Agent <span className="text-destructive">*</span>
        </Label>
        <Select
          value={task.agent}
          onValueChange={(v) => update({ agent: v })}
        >
          <SelectTrigger className="h-8 text-xs">
            <SelectValue placeholder="Assign to agent" />
          </SelectTrigger>
          <SelectContent>
            {agents.map((a) => (
              <SelectItem key={a.id} value={a.id}>
                {a.role || a.id}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-1.5">
        <div className="flex items-center gap-2">
          <Label className="text-xs">
            Expected Output <span className="text-destructive">*</span>
          </Label>
          <TemplateHelper />
        </div>
        <Textarea
          value={task.expected_output}
          onChange={(e) => update({ expected_output: e.target.value })}
          placeholder="Describe what good output looks like"
          className="text-xs min-h-[60px]"
        />
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">Output Key</Label>
        <Input
          value={task.output_key}
          onChange={(e) => update({ output_key: e.target.value })}
          placeholder="Key for downstream references (optional)"
          className="h-8 text-xs font-mono"
        />
      </div>

      <Separator />

      {/* Inputs key-value editor */}
      <KeyValueEditor
        label="Inputs"
        value={task.inputs}
        onChange={(inputs) => update({ inputs })}
      />

      <Separator />

      {/* Human review toggle */}
      <div className="space-y-3">
        <label className="flex items-center gap-2 cursor-pointer">
          <Checkbox
            checked={task.human_review}
            onCheckedChange={(checked) =>
              update({ human_review: checked === true })
            }
          />
          <span className="text-xs">Require human review after this task</span>
        </label>

        {task.human_review && (
          <div className="space-y-1.5">
            <Label className="text-xs">Review Instructions</Label>
            <Textarea
              value={task.review_instructions}
              onChange={(e) =>
                update({ review_instructions: e.target.value })
              }
              placeholder="Instructions shown to the human reviewer"
              className="text-xs min-h-[60px]"
            />
          </div>
        )}
      </div>
    </div>
  )
}

function KeyValueEditor({
  label,
  value,
  onChange,
}: {
  label: string
  value: Record<string, string>
  onChange: (value: Record<string, string>) => void
}) {
  const entries = Object.entries(value)
  const [newKey, setNewKey] = useState("")

  const addEntry = () => {
    if (newKey && !(newKey in value)) {
      onChange({ ...value, [newKey]: "" })
      setNewKey("")
    }
  }

  const updateValue = (key: string, val: string) => {
    onChange({ ...value, [key]: val })
  }

  const removeEntry = (key: string) => {
    const next = { ...value }
    delete next[key]
    onChange(next)
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Label className="text-xs">{label}</Label>
        <TemplateHelper />
      </div>
      {entries.map(([key, val]) => (
        <div key={key} className="flex items-center gap-2">
          <span className="text-[11px] font-mono text-muted-foreground min-w-[80px]">
            {key}
          </span>
          <Input
            value={val}
            onChange={(e) => updateValue(key, e.target.value)}
            className="h-7 text-xs flex-1"
            placeholder="{{input.value}}"
          />
          <button
            type="button"
            className="text-muted-foreground hover:text-destructive text-xs"
            onClick={() => removeEntry(key)}
          >
            &times;
          </button>
        </div>
      ))}
      <div className="flex items-center gap-2">
        <Input
          value={newKey}
          onChange={(e) => setNewKey(e.target.value)}
          placeholder="key name"
          className="h-7 text-xs"
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault()
              addEntry()
            }
          }}
        />
        <Button variant="outline" size="sm" className="h-7 text-xs" onClick={addEntry}>
          + Add
        </Button>
      </div>
    </div>
  )
}
