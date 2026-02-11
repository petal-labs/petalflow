import { useCallback, useEffect, useMemo, useState } from "react"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox } from "@/components/ui/checkbox"
import { useRunStore } from "@/stores/runs"
import { useProviderStore } from "@/stores/providers"
import { toast } from "sonner"
import type { Workflow } from "@/api/types"

interface RunModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  workflow: Workflow
  onStarted: (runId: string) => void
}

interface InputField {
  name: string
  type: string
  required?: boolean
  multiline?: boolean
  description?: string
  default?: unknown
}

/** Extract input fields from workflow definition's input_schema. */
function extractSchemaFields(definition: Record<string, unknown>): InputField[] {
  const schema = definition.input_schema as Record<string, unknown> | undefined
  if (!schema) return []

  // Handle JSON Schema style: { "properties": { "topic": { "type": "string" } } }
  const properties = (schema.properties ?? schema) as Record<string, unknown>
  const required = (schema.required as string[]) ?? []

  return Object.entries(properties).map(([name, spec]) => {
    const fieldSpec = spec as Record<string, unknown>
    return {
      name,
      type: String(fieldSpec.type ?? "string"),
      required: required.includes(name) || fieldSpec.required === true,
      multiline: fieldSpec.multiline === true,
      description: fieldSpec.description as string | undefined,
      default: fieldSpec.default,
    }
  })
}

/** Extract template variables from the definition JSON string. */
function extractTemplateVars(definition: Record<string, unknown>): string[] {
  const json = JSON.stringify(definition)
  const re = /\{\{input\.(\w+)\}\}/g
  const vars = new Set<string>()
  let match: RegExpExecArray | null
  while ((match = re.exec(json)) !== null) {
    vars.add(match[1])
  }
  return [...vars]
}

function InputWidget({
  field,
  value,
  onChange,
}: {
  field: InputField
  value: unknown
  onChange: (v: unknown) => void
}) {
  switch (field.type) {
    case "integer":
      return (
        <Input
          type="number"
          step="1"
          value={value !== undefined ? String(value) : ""}
          onChange={(e) => onChange(e.target.value ? Number(e.target.value) : undefined)}
          className="h-7 text-xs"
        />
      )
    case "float":
    case "number":
      return (
        <Input
          type="number"
          step="0.01"
          value={value !== undefined ? String(value) : ""}
          onChange={(e) => onChange(e.target.value ? Number(e.target.value) : undefined)}
          className="h-7 text-xs"
        />
      )
    case "boolean":
      return (
        <div className="flex items-center gap-2">
          <Checkbox
            checked={value === true}
            onCheckedChange={(checked) => onChange(checked === true)}
          />
          <span className="text-xs text-muted-foreground">
            {field.description ?? field.name}
          </span>
        </div>
      )
    case "array":
      return (
        <Input
          placeholder="comma-separated values"
          value={Array.isArray(value) ? (value as string[]).join(", ") : String(value ?? "")}
          onChange={(e) => {
            const raw = e.target.value
            onChange(raw ? raw.split(",").map((s) => s.trim()) : [])
          }}
          className="h-7 text-xs"
        />
      )
    case "object":
      return (
        <Textarea
          value={typeof value === "object" ? JSON.stringify(value, null, 2) : String(value ?? "")}
          onChange={(e) => {
            try {
              onChange(JSON.parse(e.target.value))
            } catch {
              onChange(e.target.value)
            }
          }}
          placeholder="{}"
          className="text-xs min-h-[60px] font-mono"
        />
      )
    default:
      // string
      if (field.multiline) {
        return (
          <Textarea
            value={String(value ?? "")}
            onChange={(e) => onChange(e.target.value)}
            className="text-xs min-h-[60px]"
          />
        )
      }
      return (
        <Input
          value={String(value ?? "")}
          onChange={(e) => onChange(e.target.value)}
          className="h-7 text-xs"
        />
      )
  }
}

export function RunModal({ open, onOpenChange, workflow, onStarted }: RunModalProps) {
  const startRun = useRunStore((s) => s.startRun)
  const providers = useProviderStore((s) => s.providers)
  const testResults = useProviderStore((s) => s.testResults)
  const [inputs, setInputs] = useState<Record<string, unknown>>({})
  const [trace, setTrace] = useState(true)
  const [dryRun, setDryRun] = useState(false)
  const [starting, setStarting] = useState(false)

  // Check if any referenced providers have failed tests
  const providerWarnings = useMemo(() => {
    const warnings: string[] = []
    // Collect provider names from agent definitions
    const def = workflow.definition as Record<string, unknown>
    const agents = (def.agents ?? []) as Array<Record<string, unknown>>
    const usedProviders = new Set<string>()
    for (const agent of agents) {
      if (typeof agent.provider === "string") {
        usedProviders.add(agent.provider)
      }
    }
    for (const name of usedProviders) {
      const result = testResults[name]
      if (result && !result.success) {
        warnings.push(`Provider '${name}' is not responding: ${result.error ?? "test failed"}. Check Settings > Providers.`)
      }
      const provider = providers.find((p) => p.name === name)
      if (!provider) {
        warnings.push(`Provider '${name}' is not configured. Add it in Settings > Providers.`)
      }
    }
    return warnings
  }, [workflow.definition, providers, testResults])

  // Derive input fields from schema or template vars
  const fields = useMemo<InputField[]>(() => {
    const schemaFields = extractSchemaFields(workflow.definition)
    if (schemaFields.length > 0) return schemaFields

    // Fallback: extract template variables
    const vars = extractTemplateVars(workflow.definition)
    return vars.map((name) => ({
      name,
      type: "string",
      required: true,
    }))
  }, [workflow.definition])

  // Pre-populate default values
  useEffect(() => {
    const defaults: Record<string, unknown> = {}
    const defInputs = workflow.definition.default_inputs as Record<string, unknown> | undefined
    for (const field of fields) {
      if (defInputs?.[field.name] !== undefined) {
        defaults[field.name] = defInputs[field.name]
      } else if (field.default !== undefined) {
        defaults[field.name] = field.default
      }
    }
    setInputs(defaults)
  }, [fields, workflow.definition.default_inputs])

  const setField = useCallback((name: string, value: unknown) => {
    setInputs((prev) => ({ ...prev, [name]: value }))
  }, [])

  const handleStart = useCallback(async () => {
    setStarting(true)
    try {
      const run = await startRun(workflow.id, {
        inputs: Object.keys(inputs).length > 0 ? inputs : undefined,
        trace,
        dry_run: dryRun,
      })
      onStarted(run.run_id)
      onOpenChange(false)
    } catch {
      toast.error("Failed to start run.")
    } finally {
      setStarting(false)
    }
  }, [startRun, workflow.id, inputs, trace, dryRun, onStarted, onOpenChange])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="text-sm">Run: {workflow.name}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 max-h-[60vh] overflow-y-auto">
          {/* Provider warnings */}
          {providerWarnings.length > 0 && (
            <div className="rounded border border-amber-500/50 bg-amber-500/10 px-3 py-2 space-y-1">
              {providerWarnings.map((w, i) => (
                <p key={i} className="text-xs text-amber-700 dark:text-amber-400">{w}</p>
              ))}
            </div>
          )}

          {/* Input fields */}
          {fields.length > 0 && (
            <div className="space-y-3">
              <div className="text-xs font-medium">Inputs</div>
              {fields.map((field) => (
                <div key={field.name} className="space-y-1">
                  <Label className="text-xs">
                    {field.name}
                    {field.required && (
                      <span className="text-destructive ml-0.5">*</span>
                    )}
                    <span className="ml-1 text-muted-foreground font-normal">
                      ({field.type})
                    </span>
                  </Label>
                  {field.description && field.type !== "boolean" && (
                    <p className="text-[10px] text-muted-foreground">{field.description}</p>
                  )}
                  <InputWidget
                    field={field}
                    value={inputs[field.name]}
                    onChange={(v) => setField(field.name, v)}
                  />
                </div>
              ))}
            </div>
          )}

          {fields.length === 0 && (
            <p className="text-xs text-muted-foreground">
              This workflow has no input parameters.
            </p>
          )}

          {/* Options */}
          <div className="space-y-2 pt-2 border-t">
            <div className="text-xs font-medium">Options</div>
            <label className="flex items-center gap-2 cursor-pointer">
              <Checkbox checked={trace} onCheckedChange={(c) => setTrace(c === true)} />
              <div>
                <div className="text-xs">Enable tracing</div>
                <div className="text-[10px] text-muted-foreground">
                  Captures per-node timing, inputs/outputs, LLM token usage
                </div>
              </div>
            </label>
            <label className="flex items-center gap-2 cursor-pointer">
              <Checkbox checked={dryRun} onCheckedChange={(c) => setDryRun(c === true)} />
              <div>
                <div className="text-xs">Dry run</div>
                <div className="text-[10px] text-muted-foreground">
                  Validate and compile only, no execution
                </div>
              </div>
            </label>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button size="sm" onClick={handleStart} disabled={starting}>
            {starting ? "Starting..." : "Start Run"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
