import { useState } from "react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { useWorkflowStore } from "@/stores/workflows"
import { toast } from "sonner"

interface DeleteWorkflowDialogProps {
  workflowId: string | null
  workflowName: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function DeleteWorkflowDialog({
  workflowId,
  workflowName,
  open,
  onOpenChange,
}: DeleteWorkflowDialogProps) {
  const deleteWorkflow = useWorkflowStore((s) => s.deleteWorkflow)
  const [deleting, setDeleting] = useState(false)

  const handleDelete = async () => {
    if (!workflowId) return
    setDeleting(true)
    try {
      await deleteWorkflow(workflowId)
      toast.success("Workflow deleted.")
      onOpenChange(false)
    } catch {
      toast.error("Failed to delete workflow.")
    } finally {
      setDeleting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete Workflow</DialogTitle>
          <DialogDescription>
            Are you sure you want to delete{" "}
            <span className="font-medium text-foreground">
              {workflowName}
            </span>
            ? This action cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={deleting}
          >
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={handleDelete}
            disabled={deleting}
          >
            {deleting ? "Deleting..." : "Delete"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
