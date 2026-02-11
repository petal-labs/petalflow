import { useCallback, useState } from "react"
import { Button } from "@/components/ui/button"
import { toast } from "sonner"
import { useEditorStore } from "@/stores/editor"
import { useGraphStore } from "@/stores/graph"

interface SourceTabProps {
  mode?: "agent_workflow" | "graph"
}

export function SourceTab({ mode = "agent_workflow" }: SourceTabProps) {
  const agentToDefinition = useEditorStore((s) => s.toDefinition)
  const agentLoadDefinition = useEditorStore((s) => s.loadDefinition)
  const graphToIR = useGraphStore((s) => s.toGraphIR)
  const graphLoadIR = useGraphStore((s) => s.loadFromGraphIR)

  const toJSON = mode === "graph" ? graphToIR : agentToDefinition
  const fromJSON = mode === "graph" ? graphLoadIR : agentLoadDefinition

  const [source, setSource] = useState(() =>
    JSON.stringify(toJSON(), null, 2),
  )
  const [parseError, setParseError] = useState<string | null>(null)

  const syncFromEditor = useCallback(() => {
    const def = toJSON()
    setSource(JSON.stringify(def, null, 2))
    setParseError(null)
  }, [toJSON])

  const handleApply = () => {
    try {
      const parsed = JSON.parse(source)
      fromJSON(parsed)
      setParseError(null)
      toast.success("Source applied to editor.")
    } catch (e) {
      setParseError(e instanceof Error ? e.message : "Invalid JSON")
    }
  }

  const handleCopy = async () => {
    await navigator.clipboard.writeText(source)
    toast.success("Copied to clipboard.")
  }

  const handleDownload = () => {
    const blob = new Blob([source], { type: "application/json" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = "workflow.json"
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b px-3 py-1.5">
        <div className="flex items-center gap-2">
          <span className="text-xs font-medium">
            Source ({mode === "graph" ? "Graph IR" : "JSON"})
          </span>
          {parseError && (
            <span className="text-xs text-destructive">{parseError}</span>
          )}
        </div>
        <div className="flex gap-1">
          <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={syncFromEditor}>
            Refresh
          </Button>
          <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={handleApply}>
            Apply
          </Button>
          <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={handleCopy}>
            Copy
          </Button>
          <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={handleDownload}>
            Download
          </Button>
        </div>
      </div>
      <textarea
        value={source}
        onChange={(e) => setSource(e.target.value)}
        className="flex-1 resize-none bg-muted/30 p-3 font-mono text-xs focus:outline-none"
        spellCheck={false}
      />
    </div>
  )
}
