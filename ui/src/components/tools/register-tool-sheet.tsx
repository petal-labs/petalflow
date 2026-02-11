import { useState } from "react"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { useToolStore } from "@/stores/tools"
import { toast } from "sonner"
import type { ToolTransport } from "@/api/types"

interface RegisterToolSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

type Transport = "stdio" | "sse" | "http"

interface EnvEntry {
  key: string
  value: string
}

export function RegisterToolSheet({
  open,
  onOpenChange,
}: RegisterToolSheetProps) {
  const registerTool = useToolStore((s) => s.registerTool)

  const [name, setName] = useState("")
  const [transport, setTransport] = useState<Transport>("stdio")
  const [command, setCommand] = useState("")
  const [args, setArgs] = useState("")
  const [endpointUrl, setEndpointUrl] = useState("")
  const [envVars, setEnvVars] = useState<EnvEntry[]>([])
  const [timeout, setTimeout] = useState("")

  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{
    success: boolean
    actions?: string[]
    error?: string
  } | null>(null)

  const reset = () => {
    setName("")
    setTransport("stdio")
    setCommand("")
    setArgs("")
    setEndpointUrl("")
    setEnvVars([])
    setTimeout("")
    setTesting(false)
    setTestResult(null)
  }

  const handleClose = (open: boolean) => {
    if (!open) reset()
    onOpenChange(open)
  }

  const addEnvVar = () => {
    setEnvVars((prev) => [...prev, { key: "", value: "" }])
  }

  const updateEnvVar = (
    index: number,
    field: "key" | "value",
    value: string,
  ) => {
    setEnvVars((prev) =>
      prev.map((e, i) => (i === index ? { ...e, [field]: value } : e)),
    )
  }

  const removeEnvVar = (index: number) => {
    setEnvVars((prev) => prev.filter((_, i) => i !== index))
  }

  const buildPayload = () => {
    const env: Record<string, string> = {}
    for (const e of envVars) {
      if (e.key) env[e.key] = e.value
    }
    return {
      name,
      type: transport === "sse" ? "mcp" : transport === "stdio" ? "mcp" : "http",
      transport: transport as ToolTransport,
      command: transport === "stdio" ? command : undefined,
      args:
        transport === "stdio" && args
          ? args.split(/\s+/).filter(Boolean)
          : undefined,
      endpoint_url:
        transport === "sse" || transport === "http" ? endpointUrl : undefined,
      env: Object.keys(env).length > 0 ? env : undefined,
      timeout_ms: timeout ? parseInt(timeout, 10) : undefined,
    }
  }

  const handleTestAndRegister = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      await registerTool(buildPayload())
      // After registration, fetch the tool to get discovered actions
      const tool = await useToolStore.getState().getTool(name)
      setTestResult({
        success: true,
        actions: tool.actions.map((a) => `${a.name} (${Object.keys(a.input_schema ?? {}).length} inputs)`),
      })
    } catch {
      setTestResult({ success: false, error: "Registration failed. Check your configuration." })
    } finally {
      setTesting(false)
    }
  }

  const handleConfirm = () => {
    toast.success(`Tool "${name}" registered.`)
    handleClose(false)
  }

  const canTest =
    name.length > 0 &&
    ((transport === "stdio" && command.length > 0) ||
      (transport === "sse" && endpointUrl.length > 0) ||
      (transport === "http" && endpointUrl.length > 0))

  return (
    <Sheet open={open} onOpenChange={handleClose}>
      <SheetContent className="w-[440px] sm:w-[540px] overflow-y-auto">
        <SheetHeader>
          <SheetTitle>Register Tool</SheetTitle>
          <SheetDescription>
            Configure and test a new tool registration.
          </SheetDescription>
        </SheetHeader>

        <div className="mt-6 space-y-4">
          {/* Tool Name */}
          <div className="space-y-1.5">
            <Label htmlFor="rt-name" className="text-xs">
              Tool Name <span className="text-destructive">*</span>
            </Label>
            <Input
              id="rt-name"
              placeholder="my_tool"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>

          {/* Transport */}
          <div className="space-y-1.5">
            <Label htmlFor="rt-transport" className="text-xs">
              Transport <span className="text-destructive">*</span>
            </Label>
            <Select
              value={transport}
              onValueChange={(v) => setTransport(v as Transport)}
            >
              <SelectTrigger id="rt-transport">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="stdio">MCP (Stdio)</SelectItem>
                <SelectItem value="sse">MCP (SSE)</SelectItem>
                <SelectItem value="http">HTTP</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <Separator />

          {/* Stdio fields */}
          {transport === "stdio" && (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="rt-command" className="text-xs">
                  Command <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="rt-command"
                  placeholder="npx"
                  value={command}
                  onChange={(e) => setCommand(e.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="rt-args" className="text-xs">
                  Arguments (space-separated)
                </Label>
                <Input
                  id="rt-args"
                  placeholder="-y @acme/mcp-server"
                  value={args}
                  onChange={(e) => setArgs(e.target.value)}
                />
              </div>
            </>
          )}

          {/* SSE / HTTP fields */}
          {(transport === "sse" || transport === "http") && (
            <div className="space-y-1.5">
              <Label htmlFor="rt-url" className="text-xs">
                Endpoint URL <span className="text-destructive">*</span>
              </Label>
              <Input
                id="rt-url"
                type="url"
                placeholder="https://..."
                value={endpointUrl}
                onChange={(e) => setEndpointUrl(e.target.value)}
              />
            </div>
          )}

          {/* Environment Variables (Stdio only) */}
          {transport === "stdio" && (
            <div className="space-y-2">
              <Label className="text-xs">Environment Variables</Label>
              {envVars.map((entry, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Input
                    placeholder="KEY"
                    className="flex-1"
                    value={entry.key}
                    onChange={(e) => updateEnvVar(i, "key", e.target.value)}
                  />
                  <span className="text-muted-foreground">=</span>
                  <Input
                    placeholder="value"
                    className="flex-1"
                    type="password"
                    value={entry.value}
                    onChange={(e) => updateEnvVar(i, "value", e.target.value)}
                  />
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => removeEnvVar(i)}
                    className="text-destructive hover:text-destructive h-8 w-8 p-0"
                  >
                    &times;
                  </Button>
                </div>
              ))}
              <Button variant="outline" size="sm" onClick={addEnvVar}>
                + Add Variable
              </Button>
            </div>
          )}

          {/* Timeout */}
          <div className="space-y-1.5">
            <Label htmlFor="rt-timeout" className="text-xs">
              Timeout (ms)
            </Label>
            <Input
              id="rt-timeout"
              type="number"
              placeholder="30000"
              value={timeout}
              onChange={(e) => setTimeout(e.target.value)}
            />
          </div>

          <Separator />

          {/* Test & Register */}
          {!testResult?.success && (
            <Button
              onClick={handleTestAndRegister}
              disabled={!canTest || testing}
              className="w-full"
            >
              {testing ? "Testing..." : "Test & Register"}
            </Button>
          )}

          {/* Discovery results */}
          {testResult && (
            <div
              className={`rounded-lg border p-3 text-sm ${
                testResult.success
                  ? "border-green-500/30 bg-green-500/5"
                  : "border-destructive/30 bg-destructive/5"
              }`}
            >
              {testResult.success ? (
                <>
                  <p className="font-medium text-green-600">
                    &#10003; Connected. Found {testResult.actions?.length ?? 0}{" "}
                    action{(testResult.actions?.length ?? 0) !== 1 ? "s" : ""}:
                  </p>
                  <ul className="mt-1 space-y-0.5 text-xs text-muted-foreground">
                    {testResult.actions?.map((a) => (
                      <li key={a}>&#8226; {a}</li>
                    ))}
                  </ul>
                </>
              ) : (
                <p className="text-destructive">{testResult.error}</p>
              )}
            </div>
          )}

          {testResult?.success && (
            <div className="flex gap-2">
              <Button
                variant="outline"
                className="flex-1"
                onClick={() => handleClose(false)}
              >
                Cancel
              </Button>
              <Button className="flex-1" onClick={handleConfirm}>
                Done
              </Button>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
