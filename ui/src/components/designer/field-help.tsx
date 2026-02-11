import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import docs from "@/lib/docs.json"

type DocsSection = keyof typeof docs

/**
 * Info icon tooltip that pulls its content from docs.json.
 * Usage: <FieldHelp section="agent" field="role" />
 */
export function FieldHelp({
  section,
  field,
}: {
  section: DocsSection
  field: string
}) {
  const sectionDocs = docs[section] as Record<string, string> | undefined
  const text = sectionDocs?.[field]
  if (!text) return null

  return (
    <TooltipProvider delayDuration={200}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            className="inline-flex items-center justify-center h-3.5 w-3.5 rounded-full border text-[9px] text-muted-foreground hover:text-foreground hover:border-foreground/30 transition-colors ml-1 align-middle"
            tabIndex={-1}
          >
            i
          </button>
        </TooltipTrigger>
        <TooltipContent side="top" className="max-w-[280px] text-xs">
          {text}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

/**
 * Collapsible "Learn more" section at the bottom of a panel.
 * Usage: <LearnMore section="agent" />
 */
export function LearnMore({ section }: { section: DocsSection }) {
  const sectionDocs = docs[section] as Record<string, string> | undefined
  const helpText = sectionDocs?.["_help"]
  if (!helpText) return null

  return (
    <details className="mt-4 border-t pt-3">
      <summary className="text-[11px] text-muted-foreground cursor-pointer hover:text-foreground transition-colors select-none">
        Learn more
      </summary>
      <p className="text-[11px] text-muted-foreground mt-1.5 leading-relaxed">
        {helpText}
      </p>
    </details>
  )
}
