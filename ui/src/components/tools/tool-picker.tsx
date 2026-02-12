import { useEffect, useMemo, useState } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { cn } from "@/lib/utils"
import { useToolStore } from "@/stores/tools"
import type { Tool } from "@/api/types"

interface ToolPickerProps {
  /** Currently selected action IDs in format "toolName.actionName". */
  selected: string[]
  /** Called when selection changes. */
  onChange: (selected: string[]) => void
  /** Opens the registration sheet when clicked. */
  onRegisterNew?: () => void
}

export function ToolPicker({
  selected,
  onChange,
  onRegisterNew,
}: ToolPickerProps) {
  const tools = useToolStore((s) => s.tools)
  const fetchTools = useToolStore((s) => s.fetchTools)

  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState("")

  useEffect(() => {
    fetchTools({ status: "ready", includeSchemas: true })
  }, [fetchTools])

  const filtered = useMemo(() => {
    if (!search) return tools
    const q = search.toLowerCase()
    return tools.filter(
      (t) =>
        t.name.toLowerCase().includes(q) ||
        t.actions.some((a) => a.name.toLowerCase().includes(q)),
    )
  }, [tools, search])

  const toggleAction = (actionId: string) => {
    if (selected.includes(actionId)) {
      onChange(selected.filter((s) => s !== actionId))
    } else {
      onChange([...selected, actionId])
    }
  }

  const selectedCount = selected.length

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="justify-start">
          {selectedCount === 0
            ? "Select tools..."
            : `${selectedCount} action${selectedCount !== 1 ? "s" : ""} selected`}
        </Button>
      </PopoverTrigger>
      <PopoverContent
        className="w-80 overflow-hidden p-0 !bg-popover !text-popover-foreground !opacity-100 shadow-2xl ring-1 ring-border"
        align="start"
      >
        <div className="border-b bg-muted/40 p-2">
          <Input
            placeholder="Search tools & actions..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-8 text-xs"
          />
        </div>
        <div className="max-h-64 overflow-y-auto bg-popover p-2">
          <div className="space-y-1.5 pr-1">
            {filtered.length === 0 ? (
              <p className="text-xs text-muted-foreground py-2 text-center">
                No tools found.
              </p>
            ) : (
              filtered.map((tool: Tool) => (
                <ToolGroup
                  key={tool.name}
                  tool={tool}
                  selected={selected}
                  onToggle={toggleAction}
                />
              ))
            )}
          </div>
        </div>
        {onRegisterNew && (
          <div className="border-t bg-muted/30 p-2">
            <button
              type="button"
              className="w-full text-xs text-primary hover:underline text-left"
              onClick={() => {
                setOpen(false)
                onRegisterNew()
              }}
            >
              Register new tool...
            </button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  )
}

function ToolGroup({
  tool,
  selected,
  onToggle,
}: {
  tool: Tool
  selected: string[]
  onToggle: (actionId: string) => void
}) {
  return (
    <div className="space-y-0.5 rounded-md border bg-muted/20 p-1">
      <div className="flex items-center gap-1.5 px-1 py-0.5">
        <span className="text-xs font-medium">{tool.name}</span>
        <Badge variant="secondary" className="text-[9px] px-1 py-0">
          {tool.type}
        </Badge>
      </div>
      {tool.actions.map((action) => {
        const id = `${tool.name}.${action.name}`
        const checked = selected.includes(id)
        return (
          <label
            key={id}
            className={cn(
              "flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-xs transition-colors",
              checked
                ? "bg-primary/10 text-primary"
                : "hover:bg-muted/70",
            )}
          >
            <Checkbox
              checked={checked}
              onCheckedChange={() => onToggle(id)}
            />
            <span>{action.name}</span>
            {action.description && (
              <span className={cn("truncate text-[10px]", checked ? "text-primary/80" : "text-muted-foreground")}>
                — {action.description}
              </span>
            )}
          </label>
        )
      })}
    </div>
  )
}
