import { useCallback, useState } from "react"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { useRunStore } from "@/stores/runs"
import { toast } from "sonner"

interface ReviewGatePanelProps {
  runId: string
  reviews: Array<{
    node_id: string
    gate_id: string
    instructions: string
  }>
  nodeOutputs: Record<string, string>
}

export function ReviewGatePanel({ runId, reviews, nodeOutputs }: ReviewGatePanelProps) {
  const submitReview = useRunStore((s) => s.submitReview)

  // Show the first pending review
  const review = reviews[0]
  if (!review) return null

  return (
    <ReviewGateCard
      key={review.gate_id}
      runId={runId}
      review={review}
      output={nodeOutputs[review.node_id] ?? ""}
      onSubmit={submitReview}
      queueSize={reviews.length}
    />
  )
}

function ReviewGateCard({
  runId,
  review,
  output,
  onSubmit,
  queueSize,
}: {
  runId: string
  review: { node_id: string; gate_id: string; instructions: string }
  output: string
  onSubmit: (runId: string, gateId: string, req: { action: "approve" | "reject"; feedback?: string }) => Promise<void>
  queueSize: number
}) {
  const [feedback, setFeedback] = useState("")
  const [submitting, setSubmitting] = useState(false)

  const handleAction = useCallback(
    async (action: "approve" | "reject") => {
      setSubmitting(true)
      try {
        await onSubmit(runId, review.gate_id, {
          action,
          feedback: feedback.trim() || undefined,
        })
        setFeedback("")
      } catch {
        toast.error(`Failed to ${action} review.`)
      } finally {
        setSubmitting(false)
      }
    },
    [runId, review.gate_id, feedback, onSubmit],
  )

  return (
    <div className="border-t bg-amber-500/5">
      <div className="px-4 py-3 space-y-3">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium">Human Review Required</span>
          {queueSize > 1 && (
            <span className="text-[10px] text-muted-foreground">
              ({queueSize} pending)
            </span>
          )}
        </div>

        <div className="text-xs">
          <span className="text-muted-foreground">Task: </span>
          <span className="font-medium">{review.node_id}</span>
        </div>

        {review.instructions && (
          <div className="rounded border bg-muted/30 p-2 text-xs">
            <div className="text-[10px] text-muted-foreground mb-1">Instructions:</div>
            {review.instructions}
          </div>
        )}

        {output && (
          <div className="rounded border p-2">
            <div className="text-[10px] text-muted-foreground mb-1">Agent Output:</div>
            <pre className="text-xs font-mono whitespace-pre-wrap max-h-32 overflow-y-auto">
              {output.length > 500 ? output.slice(0, 500) + "..." : output}
            </pre>
          </div>
        )}

        <div className="space-y-1">
          <label className="text-[10px] text-muted-foreground">Feedback (optional):</label>
          <Textarea
            value={feedback}
            onChange={(e) => setFeedback(e.target.value)}
            placeholder="Add feedback for the agent..."
            className="text-xs min-h-[50px]"
          />
        </div>

        <div className="flex items-center justify-end gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => handleAction("reject")}
            disabled={submitting}
          >
            Reject (re-run task)
          </Button>
          <Button
            size="sm"
            onClick={() => handleAction("approve")}
            disabled={submitting}
          >
            Approve
          </Button>
        </div>
      </div>
    </div>
  )
}
