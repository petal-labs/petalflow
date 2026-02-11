import { useState } from "react"
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { useToolStore } from "@/stores/tools"
import type { Tool, ToolAction, ToolHealthResult } from "@/api/types"

interface ToolDetailSheetProps {
  tool: Tool | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function ToolDetailSheet({
  tool,
  open,
  onOpenChange,
}: ToolDetailSheetProps) {
  if (!tool) return null

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-[480px] sm:w-[580px] overflow-y-auto">
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2">
            {tool.name}
            <StatusBadge status={tool.status} />
          </SheetTitle>
        </SheetHeader>

        <Tabs defaultValue="overview" className="mt-4">
          <TabsList className="w-full">
            <TabsTrigger value="overview" className="flex-1">
              Overview
            </TabsTrigger>
            <TabsTrigger value="actions" className="flex-1">
              Actions
            </TabsTrigger>
            <TabsTrigger value="config" className="flex-1">
              Config
            </TabsTrigger>
            <TabsTrigger value="health" className="flex-1">
              Health
            </TabsTrigger>
          </TabsList>

          <TabsContent value="overview" className="mt-4">
            <OverviewTab tool={tool} />
          </TabsContent>
          <TabsContent value="actions" className="mt-4">
            <ActionsTab tool={tool} />
          </TabsContent>
          <TabsContent value="config" className="mt-4">
            <ConfigTab tool={tool} />
          </TabsContent>
          <TabsContent value="health" className="mt-4">
            <HealthTab tool={tool} />
          </TabsContent>
        </Tabs>
      </SheetContent>
    </Sheet>
  )
}

function StatusBadge({ status }: { status: string }) {
  const variants: Record<string, string> = {
    ready: "bg-green-500/10 text-green-600",
    unhealthy: "bg-red-500/10 text-red-600",
    disabled: "bg-muted text-muted-foreground",
    unverified: "bg-yellow-500/10 text-yellow-600",
  }
  return (
    <Badge variant="secondary" className={`text-[10px] ${variants[status] ?? ""}`}>
      {status}
    </Badge>
  )
}

function OverviewTab({ tool }: { tool: Tool }) {
  return (
    <div className="space-y-3">
      <InfoRow label="Name" value={tool.name} />
      <InfoRow label="Type" value={tool.type} />
      <InfoRow label="Transport" value={tool.transport} />
      {tool.version && <InfoRow label="Version" value={tool.version} />}
      {tool.author && <InfoRow label="Author" value={tool.author} />}
      {tool.description && (
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground">Description</p>
          <p className="text-sm">{tool.description}</p>
        </div>
      )}
      <InfoRow
        label="Actions"
        value={`${tool.actions.length} action${tool.actions.length !== 1 ? "s" : ""}`}
      />
    </div>
  )
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-medium">{value}</span>
    </div>
  )
}

function ActionsTab({ tool }: { tool: Tool }) {
  return (
    <div className="space-y-3">
      {tool.actions.length === 0 ? (
        <p className="text-sm text-muted-foreground">No actions discovered.</p>
      ) : (
        tool.actions.map((action) => (
          <ActionCard key={action.name} toolName={tool.name} action={action} />
        ))
      )}
    </div>
  )
}

function ActionCard({
  toolName,
  action,
}: {
  toolName: string
  action: ToolAction
}) {
  const testActionFn = useToolStore((s) => s.testAction)
  const [expanded, setExpanded] = useState(false)
  const [inputs, setInputs] = useState<Record<string, string>>({})
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<Record<string, unknown> | null>(null)
  const [error, setError] = useState<string | null>(null)

  const inputFields = Object.entries(action.input_schema ?? {})

  const handleTest = async () => {
    setTesting(true)
    setResult(null)
    setError(null)
    try {
      const parsed: Record<string, unknown> = {}
      for (const [k, v] of Object.entries(inputs)) {
        try {
          parsed[k] = JSON.parse(v)
        } catch {
          parsed[k] = v
        }
      }
      const res = await testActionFn(toolName, {
        action: action.name,
        inputs: parsed,
      })
      setResult(res)
    } catch {
      setError("Test failed.")
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="rounded-lg border p-3">
      <button
        type="button"
        className="flex w-full items-center justify-between text-sm font-medium"
        onClick={() => setExpanded(!expanded)}
      >
        <span>{action.name}</span>
        <span className="text-xs text-muted-foreground">
          {inputFields.length} input{inputFields.length !== 1 ? "s" : ""}
        </span>
      </button>
      {action.description && (
        <p className="text-xs text-muted-foreground mt-1">
          {action.description}
        </p>
      )}

      {expanded && (
        <div className="mt-3 space-y-3">
          <Separator />
          {inputFields.length > 0 && (
            <div className="space-y-2">
              <p className="text-xs font-medium text-muted-foreground">
                Inputs:
              </p>
              {inputFields.map(([key, schema]) => (
                <div key={key} className="space-y-1">
                  <Label className="text-[11px]">
                    {key}{" "}
                    <span className="text-muted-foreground">
                      ({typeof schema === "object" && schema && "type" in schema
                        ? String((schema as Record<string, unknown>).type)
                        : "any"})
                    </span>
                  </Label>
                  <Input
                    className="h-7 text-xs"
                    value={inputs[key] ?? ""}
                    onChange={(e) =>
                      setInputs((prev) => ({ ...prev, [key]: e.target.value }))
                    }
                  />
                </div>
              ))}
            </div>
          )}

          <Button
            size="sm"
            variant="outline"
            onClick={handleTest}
            disabled={testing}
          >
            {testing ? "Testing..." : "Test Action"}
          </Button>

          {result && (
            <pre className="rounded bg-muted p-2 text-xs overflow-auto max-h-40">
              {JSON.stringify(result, null, 2)}
            </pre>
          )}
          {error && <p className="text-xs text-destructive">{error}</p>}
        </div>
      )}
    </div>
  )
}

function ConfigTab({ tool }: { tool: Tool }) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        Configuration editing for <span className="font-medium">{tool.name}</span>.
      </p>
      <div className="rounded-lg border p-3 text-xs text-muted-foreground">
        Transport: {tool.transport}
        <br />
        Type: {tool.type}
      </div>
      <p className="text-xs text-muted-foreground">
        Editable configuration fields will appear here based on the tool's
        config schema.
      </p>
    </div>
  )
}

function HealthTab({ tool }: { tool: Tool }) {
  const checkHealth = useToolStore((s) => s.checkHealth)
  const healthResults = useToolStore((s) => s.healthResults)
  const [checking, setChecking] = useState(false)

  const result: ToolHealthResult | undefined = healthResults[tool.name]

  const handleCheck = async () => {
    setChecking(true)
    try {
      await checkHealth(tool.name)
    } catch {
      // stored in store
    } finally {
      setChecking(false)
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Health Status</h3>
        <Button
          size="sm"
          variant="outline"
          onClick={handleCheck}
          disabled={checking}
        >
          {checking ? "Checking..." : "Check Now"}
        </Button>
      </div>

      {result ? (
        <div className="rounded-lg border p-3 space-y-2">
          <div className="flex items-center gap-2">
            <StatusBadge status={result.status} />
            <span className="text-sm">{result.latency_ms}ms</span>
          </div>
          <p className="text-xs text-muted-foreground">
            Last checked: {new Date(result.checked_at).toLocaleString()}
          </p>
          {result.error && (
            <p className="text-xs text-destructive">{result.error}</p>
          )}
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          No health data yet. Click "Check Now" to run a health check.
        </p>
      )}
    </div>
  )
}
