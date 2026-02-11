import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"

export function TemplateHelper() {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="h-5 px-1.5 text-[10px] font-mono"
        >
          {"{{ }}"}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-72 text-xs" align="start">
        <h4 className="font-semibold mb-2">Template Syntax</h4>
        <div className="space-y-2 text-muted-foreground">
          <div>
            <p className="font-medium text-foreground">Input variables</p>
            <code className="text-[11px] bg-muted px-1 rounded">
              {"{{input.topic}}"}
            </code>
            <p className="mt-0.5">References a workflow input parameter.</p>
          </div>
          <div>
            <p className="font-medium text-foreground">Task outputs</p>
            <code className="text-[11px] bg-muted px-1 rounded">
              {"{{tasks.research.output}}"}
            </code>
            <p className="mt-0.5">References the output of a previous task.</p>
          </div>
          <div>
            <p className="font-medium text-foreground">Examples</p>
            <pre className="bg-muted rounded p-1.5 text-[10px] whitespace-pre-wrap">
{`Research {{input.topic}} and provide
a summary.

Using the findings from
{{tasks.gather_data.output}},
write a report.`}
            </pre>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}
