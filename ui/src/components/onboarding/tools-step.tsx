import { useEffect, useState } from "react"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useToolStore } from "@/stores/tools"
import {
  mcpServerDefs,
  type McpServerDef,
  type McpCredential,
} from "@/lib/mcp-server-defs"
import type { Tool } from "@/api/types"

type InstallStatus = "idle" | "installing" | "success" | "error"

interface ServerState {
  checked: boolean
  status: InstallStatus
  error?: string
}

export function ToolsStep() {
  const tools = useToolStore((s) => s.tools)
  const fetchTools = useToolStore((s) => s.fetchTools)
  const registerTool = useToolStore((s) => s.registerTool)

  const [servers, setServers] = useState<Record<string, ServerState>>(() => {
    const init: Record<string, ServerState> = {}
    for (const def of mcpServerDefs) {
      init[def.id] = { checked: false, status: "idle" }
    }
    return init
  })

  // Credential modal state
  const [credQueue, setCredQueue] = useState<McpServerDef[]>([])
  const [credValues, setCredValues] = useState<Record<string, string>>({})
  const currentCredServer = credQueue[0] ?? null

  useEffect(() => {
    fetchTools()
  }, [fetchTools])

  const toggleServer = (id: string) => {
    setServers((prev) => ({
      ...prev,
      [id]: { ...prev[id], checked: !prev[id].checked },
    }))
  }

  const selectedCount = Object.values(servers).filter((s) => s.checked).length

  const handleInstallSelected = () => {
    const selected = mcpServerDefs.filter((d) => servers[d.id].checked)
    if (selected.length === 0) return

    // Find servers that need credentials
    const needsCreds = selected.filter(
      (d) => d.credentials && d.credentials.length > 0,
    )
    const noCreds = selected.filter(
      (d) => !d.credentials || d.credentials.length === 0,
    )

    // Install servers that don't need credentials immediately
    for (const def of noCreds) {
      installServer(def, {})
    }

    // Queue servers that need credentials
    if (needsCreds.length > 0) {
      setCredQueue(needsCreds)
      setCredValues({})
    }
  }

  const handleCredSubmit = () => {
    if (!currentCredServer) return
    const env: Record<string, string> = {}
    for (const cred of currentCredServer.credentials ?? []) {
      env[cred.envVar] = credValues[cred.envVar] ?? ""
    }
    installServer(currentCredServer, env)
    setCredQueue((q) => q.slice(1))
    setCredValues({})
  }

  const handleCredSkip = () => {
    if (!currentCredServer) return
    // Uncheck the server since we're skipping it
    setServers((prev) => ({
      ...prev,
      [currentCredServer.id]: { ...prev[currentCredServer.id], checked: false },
    }))
    setCredQueue((q) => q.slice(1))
    setCredValues({})
  }

  const installServer = async (
    def: McpServerDef,
    env: Record<string, string>,
  ) => {
    setServers((prev) => ({
      ...prev,
      [def.id]: { ...prev[def.id], status: "installing" },
    }))
    try {
      await registerTool({
        name: def.id,
        type: "mcp",
        transport: "stdio",
        command: def.command,
        args: def.args,
        env: Object.keys(env).length > 0 ? env : undefined,
      })
      setServers((prev) => ({
        ...prev,
        [def.id]: { ...prev[def.id], status: "success" },
      }))
    } catch {
      setServers((prev) => ({
        ...prev,
        [def.id]: { ...prev[def.id], status: "error", error: "Installation failed" },
      }))
    }
  }

  const isAlreadyRegistered = (id: string) =>
    tools.some((t: Tool) => t.name === id)

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Register Tools</CardTitle>
          <CardDescription>
            Tools extend what your agents can do — search the web, read files,
            connect to APIs, and more. This step is optional.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {/* Popular MCP servers */}
          <div className="rounded-lg border p-4 space-y-3">
            <h3 className="text-sm font-medium">
              Popular MCP Servers (one-click install)
            </h3>
            <div className="space-y-2">
              {mcpServerDefs.map((def) => {
                const state = servers[def.id]
                const registered = isAlreadyRegistered(def.id)
                return (
                  <label
                    key={def.id}
                    className="flex items-center gap-3 rounded-md px-2 py-1.5 hover:bg-muted/50 cursor-pointer"
                  >
                    <Checkbox
                      checked={state.checked || registered}
                      disabled={
                        registered ||
                        state.status === "installing" ||
                        state.status === "success"
                      }
                      onCheckedChange={() => toggleServer(def.id)}
                    />
                    <div className="flex-1 min-w-0">
                      <span className="text-sm font-medium">{def.name}</span>
                      <span className="text-xs text-muted-foreground ml-2">
                        — {def.description}
                      </span>
                    </div>
                    {/* Status indicator */}
                    {state.status === "installing" && (
                      <span className="text-xs text-muted-foreground animate-pulse">
                        Installing...
                      </span>
                    )}
                    {(state.status === "success" || registered) && (
                      <span className="text-xs text-green-600">&#10003;</span>
                    )}
                    {state.status === "error" && (
                      <span className="text-xs text-destructive">
                        &#10007; {state.error}
                      </span>
                    )}
                  </label>
                )
              })}
            </div>
            <Button
              size="sm"
              onClick={handleInstallSelected}
              disabled={selectedCount === 0}
            >
              Install Selected{selectedCount > 0 ? ` (${selectedCount})` : ""}
            </Button>
          </div>

          {/* Registered tools */}
          {tools.length > 0 && (
            <div className="space-y-2">
              <h3 className="text-xs font-medium text-muted-foreground">
                Registered tools:
              </h3>
              {tools.map((t: Tool) => (
                <div
                  key={t.name}
                  className="flex items-center gap-2 text-sm px-2"
                >
                  <span className="text-green-500">&#10003;</span>
                  <span className="font-medium">{t.name}</span>
                  <span className="text-xs text-muted-foreground">
                    — {t.actions.length} action
                    {t.actions.length !== 1 ? "s" : ""} ({t.status})
                  </span>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Credential prompt modal */}
      <Dialog
        open={currentCredServer !== null}
        onOpenChange={(open) => {
          if (!open) handleCredSkip()
        }}
      >
        {currentCredServer && (
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                Credentials for {currentCredServer.name}
              </DialogTitle>
              <DialogDescription>
                This server requires credentials to connect.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4">
              {(currentCredServer.credentials ?? []).map(
                (cred: McpCredential) => (
                  <div key={cred.envVar} className="space-y-1.5">
                    <Label htmlFor={`cred-${cred.envVar}`} className="text-xs">
                      {cred.label}
                    </Label>
                    <Input
                      id={`cred-${cred.envVar}`}
                      type={cred.type}
                      placeholder={cred.placeholder}
                      value={credValues[cred.envVar] ?? ""}
                      onChange={(e) =>
                        setCredValues((prev) => ({
                          ...prev,
                          [cred.envVar]: e.target.value,
                        }))
                      }
                    />
                  </div>
                ),
              )}
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={handleCredSkip}>
                Skip
              </Button>
              <Button onClick={handleCredSubmit}>
                Install
              </Button>
            </DialogFooter>
          </DialogContent>
        )}
      </Dialog>
    </>
  )
}
