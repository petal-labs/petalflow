import { useEffect } from "react"
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
import { ToolPicker } from "@/components/tools/tool-picker"
import { FieldHelp, LearnMore } from "@/components/designer/field-help"
import { useEditorStore, type AgentDef } from "@/stores/editor"
import { useProviderStore } from "@/stores/providers"
import { providerDefs } from "@/lib/provider-defs"

interface AgentFormProps {
  agent: AgentDef
  onRegisterTool?: () => void
}

export function AgentForm({ agent, onRegisterTool }: AgentFormProps) {
  const updateAgent = useEditorStore((s) => s.updateAgent)
  const removeAgent = useEditorStore((s) => s.removeAgent)
  const providers = useProviderStore((s) => s.providers)
  const fetchProviders = useProviderStore((s) => s.fetchProviders)

  useEffect(() => {
    if (providers.length === 0) fetchProviders()
  }, [providers.length, fetchProviders])

  const update = (patch: Partial<AgentDef>) => updateAgent(agent.id, patch)

  const selectedProviderDef = providerDefs.find((p) => p.id === agent.provider)
  const modelList = selectedProviderDef?.models ?? []

  const slugify = (text: string) =>
    text
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^_|_$/g, "")
      .slice(0, 64)

  return (
    <div className="space-y-4 p-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">Agent</h3>
        <Button
          variant="ghost"
          size="sm"
          className="text-destructive hover:text-destructive h-6 text-xs"
          onClick={() => removeAgent(agent.id)}
        >
          Remove
        </Button>
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">ID</Label>
        <Input
          value={agent.id}
          onChange={(e) => update({ id: e.target.value })}
          className="h-8 text-xs font-mono"
        />
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">
          Role <span className="text-destructive">*</span>
          <FieldHelp section="agent" field="role" />
        </Label>
        <Input
          value={agent.role}
          onChange={(e) => {
            const role = e.target.value
            // Auto-slug ID if it looks auto-generated
            if (agent.id.startsWith("agent_")) {
              update({ role, id: slugify(role) || agent.id })
            } else {
              update({ role })
            }
          }}
          placeholder="e.g., Senior Research Analyst"
          className="h-8 text-xs"
        />
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">
          Goal <span className="text-destructive">*</span>
          <FieldHelp section="agent" field="goal" />
        </Label>
        <Input
          value={agent.goal}
          onChange={(e) => update({ goal: e.target.value })}
          placeholder="What this agent aims to accomplish"
          className="h-8 text-xs"
        />
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">
          Backstory
          <FieldHelp section="agent" field="backstory" />
        </Label>
        <Textarea
          value={agent.backstory}
          onChange={(e) => update({ backstory: e.target.value })}
          placeholder="Background context for the agent (optional)"
          className="text-xs min-h-[60px]"
        />
      </div>

      <Separator />

      <div className="space-y-1.5">
        <Label className="text-xs">
          Provider <span className="text-destructive">*</span>
          <FieldHelp section="agent" field="provider" />
        </Label>
        <Select
          value={agent.provider}
          onValueChange={(v) => {
            const provider = providers.find((p) => p.name === v)
            const staticModels = providerDefs.find((d) => d.id === v)?.models ?? []
            const defaultModel =
              (provider?.default_model && provider.default_model.trim()) ||
              staticModels[0] ||
              ""
            update({ provider: v, model: defaultModel })
          }}
        >
          <SelectTrigger className="h-8 text-xs">
            <SelectValue placeholder="Select provider" />
          </SelectTrigger>
          <SelectContent>
            {providers.map((p) => (
              <SelectItem key={p.name} value={p.name}>
                {providerDefs.find((d) => d.id === p.name)?.label ?? p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">
          Model <span className="text-destructive">*</span>
          <FieldHelp section="agent" field="model" />
        </Label>
        {modelList.length > 0 ? (
          <Select
            value={agent.model}
            onValueChange={(v) => update({ model: v })}
          >
            <SelectTrigger className="h-8 text-xs">
              <SelectValue placeholder="Select model" />
            </SelectTrigger>
            <SelectContent>
              {modelList.map((m) => (
                <SelectItem key={m} value={m}>
                  {m}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input
            value={agent.model}
            onChange={(e) => update({ model: e.target.value })}
            placeholder="Enter model name"
            className="h-8 text-xs"
          />
        )}
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">
          Tools
          <FieldHelp section="agent" field="tools" />
        </Label>
        <ToolPicker
          selected={agent.tools}
          onChange={(tools) => update({ tools })}
          onRegisterNew={onRegisterTool}
        />
      </div>

      <Separator />

      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1.5">
          <Label className="text-xs">
            Temperature
            <FieldHelp section="agent" field="temperature" />
          </Label>
          <Input
            type="number"
            min={0}
            max={2}
            step={0.1}
            value={agent.temperature ?? ""}
            onChange={(e) =>
              update({
                temperature: e.target.value
                  ? parseFloat(e.target.value)
                  : undefined,
              })
            }
            placeholder="0.7"
            className="h-8 text-xs"
          />
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs">
            Max Tokens
            <FieldHelp section="agent" field="max_tokens" />
          </Label>
          <Input
            type="number"
            value={agent.max_tokens ?? ""}
            onChange={(e) =>
              update({
                max_tokens: e.target.value
                  ? parseInt(e.target.value, 10)
                  : undefined,
              })
            }
            placeholder="4096"
            className="h-8 text-xs"
          />
        </div>
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs">
          System Prompt Override
          <FieldHelp section="agent" field="system_prompt" />
        </Label>
        <Textarea
          value={agent.system_prompt ?? ""}
          onChange={(e) => update({ system_prompt: e.target.value || undefined })}
          placeholder="Override the generated system prompt (optional)"
          className="text-xs min-h-[60px]"
        />
      </div>

      <LearnMore section="agent" />
    </div>
  )
}
