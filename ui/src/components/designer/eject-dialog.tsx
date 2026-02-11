import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"

interface EjectDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

export function EjectDialog({ open, onOpenChange, onConfirm }: EjectDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Eject to Graph Mode</DialogTitle>
          <DialogDescription>
            This will compile your Agent/Task workflow into a Graph IR and switch
            the workflow to graph mode. The graph becomes the source of truth —
            you will not be able to switch back to Agent/Task mode.
          </DialogDescription>
        </DialogHeader>
        <div className="rounded border bg-muted/30 p-3 text-xs space-y-1">
          <div className="font-medium">What happens:</div>
          <ul className="list-disc pl-4 space-y-0.5 text-muted-foreground">
            <li>Your agents and tasks are compiled into graph nodes and edges</li>
            <li>The workflow kind changes from &quot;agent_workflow&quot; to &quot;graph&quot;</li>
            <li>You gain full control over individual nodes, ports, and connections</li>
            <li>This action cannot be undone</li>
          </ul>
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={() => {
              onConfirm()
              onOpenChange(false)
            }}
          >
            Eject to Graph
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
