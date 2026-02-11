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
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Separator } from "@/components/ui/separator"
import type { Workflow } from "@/api/types"

interface WorkflowSettingsPanelProps {
  workflow: Workflow
  open: boolean
  onOpenChange: (open: boolean) => void
  onUpdate: (patch: Partial<Pick<Workflow, "name" | "description" | "tags">>) => void
}

export function WorkflowSettingsPanel({
  workflow,
  open,
  onOpenChange,
  onUpdate,
}: WorkflowSettingsPanelProps) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-[380px] overflow-y-auto">
        <SheetHeader>
          <SheetTitle>Workflow Settings</SheetTitle>
        </SheetHeader>

        <div className="mt-6 space-y-4">
          <div className="space-y-1.5">
            <Label className="text-xs">Name</Label>
            <Input
              value={workflow.name}
              onChange={(e) => onUpdate({ name: e.target.value })}
              className="h-8 text-xs"
            />
          </div>

          <div className="space-y-1.5">
            <Label className="text-xs">Description</Label>
            <Textarea
              value={workflow.description ?? ""}
              onChange={(e) => onUpdate({ description: e.target.value })}
              placeholder="What this workflow does"
              className="text-xs min-h-[60px]"
            />
          </div>

          <div className="space-y-1.5">
            <Label className="text-xs">Tags (comma-separated)</Label>
            <Input
              value={(workflow.tags ?? []).join(", ")}
              onChange={(e) =>
                onUpdate({
                  tags: e.target.value
                    .split(",")
                    .map((t) => t.trim())
                    .filter(Boolean),
                })
              }
              placeholder="research, llm, analysis"
              className="h-8 text-xs"
            />
          </div>

          <Separator />

          <div className="space-y-1.5">
            <Label className="text-xs">Error Strategy</Label>
            <Select defaultValue="fail_fast">
              <SelectTrigger className="h-8 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="fail_fast">Fail Fast</SelectItem>
                <SelectItem value="continue_on_error">
                  Continue on Error
                </SelectItem>
                <SelectItem value="retry_failed_nodes">
                  Retry Failed Nodes
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-1.5">
            <Label className="text-xs">Timeout (seconds)</Label>
            <Input
              type="number"
              placeholder="300"
              className="h-8 text-xs"
            />
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
