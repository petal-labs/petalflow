import { useCallback } from "react"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { useGraphStore, type GraphNodeData, type PortHandle } from "@/stores/graph"
import { useProviderStore } from "@/stores/providers"
import { providerDefs } from "@/lib/provider-defs"
import { ToolPicker } from "@/components/tools/tool-picker"

interface NodeInspectorProps {
  nodeId: string
}

export function NodeInspector({ nodeId }: NodeInspectorProps) {
  const node = useGraphStore((s) => s.nodes.find((n) => n.id === nodeId))
  const updateNodeData = useGraphStore((s) => s.updateNodeData)
  const updateNodeConfig = useGraphStore((s) => s.updateNodeConfig)
  const removeNodes = useGraphStore((s) => s.removeNodes)

  const providers = useProviderStore((s) => s.providers)

  const setConfig = useCallback(
    (key: string, value: unknown) => {
      updateNodeConfig(nodeId, { [key]: value })
    },
    [nodeId, updateNodeConfig],
  )

  if (!node) {
    return (
      <div className="flex h-full items-center justify-center p-4 text-xs text-muted-foreground">
        Node not found
      </div>
    )
  }

  const data = node.data as GraphNodeData
  const config = data.config
  const isLLM = data.kind === "llm_prompt" || data.category === "LLM"
  const isTool = data.category === "Tools"

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b px-3 py-2">
        <div>
          <div className="text-xs font-medium">{data.label}</div>
          <div className="text-[10px] text-muted-foreground">{data.kind}</div>
        </div>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 text-xs text-destructive"
          onClick={() => removeNodes([nodeId])}
        >
          Delete
        </Button>
      </div>

      <div className="flex-1 overflow-y-auto p-3 space-y-3">
        {/* Common fields */}
        <div className="space-y-1.5">
          <Label className="text-xs">Label</Label>
          <Input
            value={data.label}
            onChange={(e) => updateNodeData(nodeId, { label: e.target.value })}
            className="h-7 text-xs"
          />
        </div>

        <Separator />

        {/* LLM-specific fields */}
        {isLLM && (
          <>
            <div className="space-y-1.5">
              <Label className="text-xs">Provider</Label>
              <Select
                value={String(config.provider ?? "")}
                onValueChange={(v) => setConfig("provider", v)}
              >
                <SelectTrigger className="h-7 text-xs">
                  <SelectValue placeholder="Select provider" />
                </SelectTrigger>
                <SelectContent>
                  {providers.map((p) => (
                    <SelectItem key={p.name} value={p.name}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs">Model</Label>
              <Select
                value={String(config.model ?? "")}
                onValueChange={(v) => setConfig("model", v)}
              >
                <SelectTrigger className="h-7 text-xs">
                  <SelectValue placeholder="Select model" />
                </SelectTrigger>
                <SelectContent>
                  {(() => {
                    const prov = String(config.provider ?? "")
                    const def = providerDefs.find(
                      (d) => d.id.toLowerCase() === prov.toLowerCase(),
                    )
                    const models = def?.models ?? []
                    return models.map((m) => (
                      <SelectItem key={m} value={m}>
                        {m}
                      </SelectItem>
                    ))
                  })()}
                </SelectContent>
              </Select>
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs">System Prompt</Label>
              <Textarea
                value={String(config.system_prompt ?? "")}
                onChange={(e) => setConfig("system_prompt", e.target.value)}
                placeholder="System instructions..."
                className="text-xs min-h-[60px]"
              />
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs">Prompt Template</Label>
              <Textarea
                value={String(config.prompt_template ?? "")}
                onChange={(e) => setConfig("prompt_template", e.target.value)}
                placeholder="{{.input}}"
                className="text-xs min-h-[80px] font-mono"
              />
            </div>

            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1.5">
                <Label className="text-xs">Temperature</Label>
                <Input
                  type="number"
                  step="0.1"
                  min="0"
                  max="2"
                  value={String(config.temperature ?? "")}
                  onChange={(e) =>
                    setConfig(
                      "temperature",
                      e.target.value ? Number(e.target.value) : undefined,
                    )
                  }
                  placeholder="0.7"
                  className="h-7 text-xs"
                />
              </div>
              <div className="space-y-1.5">
                <Label className="text-xs">Max Tokens</Label>
                <Input
                  type="number"
                  value={String(config.max_tokens ?? "")}
                  onChange={(e) =>
                    setConfig(
                      "max_tokens",
                      e.target.value ? Number(e.target.value) : undefined,
                    )
                  }
                  placeholder="4096"
                  className="h-7 text-xs"
                />
              </div>
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs">Tools</Label>
              <ToolPicker
                selected={(config.tools as string[]) ?? []}
                onChange={(tools) => setConfig("tools", tools)}
              />
            </div>
          </>
        )}

        {/* Tool-specific fields */}
        {isTool && (
          <>
            {config.action !== undefined && (
              <div className="space-y-1.5">
                <Label className="text-xs">Action</Label>
                <Input
                  value={String(config.action ?? "")}
                  onChange={(e) => setConfig("action", e.target.value)}
                  className="h-7 text-xs"
                />
              </div>
            )}

            <div className="space-y-1.5">
              <Label className="text-xs">Input Mapping</Label>
              <Textarea
                value={String(config.input_mapping ?? "")}
                onChange={(e) => setConfig("input_mapping", e.target.value)}
                placeholder="JSON field mapping..."
                className="text-xs min-h-[60px] font-mono"
              />
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs">Config Overrides</Label>
              <Textarea
                value={
                  typeof config.overrides === "object"
                    ? JSON.stringify(config.overrides, null, 2)
                    : String(config.overrides ?? "")
                }
                onChange={(e) => {
                  try {
                    setConfig("overrides", JSON.parse(e.target.value))
                  } catch {
                    setConfig("overrides", e.target.value)
                  }
                }}
                placeholder="{}"
                className="text-xs min-h-[60px] font-mono"
              />
            </div>
          </>
        )}

        {/* Generic config for other node kinds */}
        {!isLLM && !isTool && (
          <div className="space-y-1.5">
            <Label className="text-xs">Configuration (JSON)</Label>
            <Textarea
              value={JSON.stringify(config, null, 2)}
              onChange={(e) => {
                try {
                  const parsed = JSON.parse(e.target.value)
                  updateNodeConfig(nodeId, parsed)
                } catch {
                  // ignore parse errors during editing
                }
              }}
              className="text-xs min-h-[100px] font-mono"
            />
          </div>
        )}

        <Separator />

        {/* Port configuration */}
        <div className="space-y-2">
          <div className="text-xs font-medium">Ports</div>
          <PortSection
            title="Inputs"
            ports={data.inputPorts}
            onChange={(ports) => updateNodeData(nodeId, { inputPorts: ports })}
          />
          <PortSection
            title="Outputs"
            ports={data.outputPorts}
            onChange={(ports) => updateNodeData(nodeId, { outputPorts: ports })}
          />
        </div>
      </div>
    </div>
  )
}

function PortSection({
  title,
  ports,
  onChange,
}: {
  title: string
  ports: PortHandle[]
  onChange: (ports: PortHandle[]) => void
}) {
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <span className="text-[10px] text-muted-foreground">{title}</span>
        <Button
          variant="ghost"
          size="sm"
          className="h-5 text-[10px] px-1"
          onClick={() => {
            onChange([
              ...ports,
              { name: `port_${ports.length + 1}`, type: "string" },
            ])
          }}
        >
          + Add
        </Button>
      </div>
      {ports.map((port, i) => (
        <div key={i} className="flex items-center gap-1">
          <Input
            value={port.name}
            onChange={(e) => {
              const next = [...ports]
              next[i] = { ...port, name: e.target.value }
              onChange(next)
            }}
            className="h-6 text-[10px] flex-1"
            placeholder="name"
          />
          <Select
            value={port.type}
            onValueChange={(v) => {
              const next = [...ports]
              next[i] = { ...port, type: v }
              onChange(next)
            }}
          >
            <SelectTrigger className="h-6 text-[10px] w-20">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {["string", "number", "boolean", "object", "array", "any"].map(
                (t) => (
                  <SelectItem key={t} value={t}>
                    {t}
                  </SelectItem>
                ),
              )}
            </SelectContent>
          </Select>
          <Button
            variant="ghost"
            size="sm"
            className="h-5 w-5 p-0 text-[10px] text-destructive"
            onClick={() => {
              onChange(ports.filter((_, idx) => idx !== i))
            }}
          >
            &times;
          </Button>
        </div>
      ))}
    </div>
  )
}
