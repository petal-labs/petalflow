import { useState } from "react"
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
import { Badge } from "@/components/ui/badge"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { cn } from "@/lib/utils"
import { providerDefs, type ProviderDef } from "@/lib/provider-defs"
import { useProviderStore } from "@/stores/providers"
import type { Provider } from "@/api/types"

interface FormValues {
  [key: string]: string
}

export function ProvidersStep() {
  const providers = useProviderStore((s) => s.providers)
  const testResults = useProviderStore((s) => s.testResults)
  const createProvider = useProviderStore((s) => s.createProvider)
  const deleteProvider = useProviderStore((s) => s.deleteProvider)
  const testProvider = useProviderStore((s) => s.testProvider)
  const fetchProviders = useProviderStore((s) => s.fetchProviders)

  const [selected, setSelected] = useState<ProviderDef | null>(null)
  const [formValues, setFormValues] = useState<FormValues>({})
  const [selectedModel, setSelectedModel] = useState("")
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const handleSelectProvider = (def: ProviderDef) => {
    setSelected(def)
    setError(null)
    // Pre-fill defaults
    const defaults: FormValues = {}
    for (const f of def.fields) {
      if (f.defaultValue) defaults[f.name] = f.defaultValue
    }
    setFormValues(defaults)
    setSelectedModel(def.models[0] ?? "")
  }

  const handleFieldChange = (name: string, value: string) => {
    setFormValues((prev) => ({ ...prev, [name]: value }))
  }

  const handleSave = async () => {
    if (!selected) return
    setError(null)
    setSaving(true)
    try {
      await createProvider({
        name: selected.id,
        api_key: formValues.api_key ?? "",
        default_model: selectedModel || formValues.default_model || "",
        base_url: formValues.base_url,
        organization_id: formValues.organization_id,
        project_id: formValues.project_id,
      })
      await fetchProviders()
      setSelected(null)
      setFormValues({})
    } catch {
      setError("Failed to save provider configuration.")
    } finally {
      setSaving(false)
    }
  }

  const handleTest = async (name: string) => {
    setTesting(name)
    try {
      await testProvider(name)
    } catch {
      // testProvider stores the result in the store
    } finally {
      setTesting(null)
    }
  }

  const handleRemove = async (name: string) => {
    await deleteProvider(name)
  }

  const isConfigured = (id: string) => providers.some((p: Provider) => p.name === id)
  const configuredCount = providers.length

  return (
    <Card>
      <CardHeader>
        <CardTitle>Configure LLM Providers</CardTitle>
        <CardDescription>
          Select a provider to configure. At least one is required to build
          workflows.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        {/* Provider cards grid */}
        <div className="grid grid-cols-3 gap-2 sm:grid-cols-4">
          {providerDefs.map((def) => {
            const configured = isConfigured(def.id)
            return (
              <button
                key={def.id}
                type="button"
                onClick={() => !configured && handleSelectProvider(def)}
                className={cn(
                  "flex flex-col items-center gap-1 rounded-lg border p-3 text-center text-xs transition-colors",
                  selected?.id === def.id
                    ? "border-primary bg-primary/5"
                    : configured
                      ? "border-green-500/50 bg-green-500/5 cursor-default"
                      : "hover:border-primary/50 hover:bg-muted/50 cursor-pointer",
                )}
              >
                <span className="font-medium text-foreground">{def.label}</span>
                <span className="text-muted-foreground">{def.description}</span>
                {configured && (
                  <Badge
                    variant="secondary"
                    className="mt-1 bg-green-500/10 text-green-600 text-[10px]"
                  >
                    Configured
                  </Badge>
                )}
              </button>
            )
          })}
        </div>

        {/* Configuration form for selected provider */}
        {selected && !isConfigured(selected.id) && (
          <div className="rounded-lg border p-4 space-y-4">
            <h3 className="text-sm font-medium">
              {selected.label} Configuration
            </h3>

            {selected.fields.map((field) => (
              <div key={field.name} className="space-y-1.5">
                <Label htmlFor={`prov-${field.name}`} className="text-xs">
                  {field.label}
                  {field.required && (
                    <span className="text-destructive ml-0.5">*</span>
                  )}
                </Label>
                <Input
                  id={`prov-${field.name}`}
                  type={field.type}
                  placeholder={field.placeholder}
                  value={formValues[field.name] ?? ""}
                  onChange={(e) =>
                    handleFieldChange(field.name, e.target.value)
                  }
                />
              </div>
            ))}

            {selected.models.length > 0 && (
              <div className="space-y-1.5">
                <Label htmlFor="prov-model" className="text-xs">
                  Default Model
                  <span className="text-destructive ml-0.5">*</span>
                </Label>
                <Select value={selectedModel} onValueChange={setSelectedModel}>
                  <SelectTrigger id="prov-model">
                    <SelectValue placeholder="Select a model" />
                  </SelectTrigger>
                  <SelectContent>
                    {selected.models.map((m) => (
                      <SelectItem key={m} value={m}>
                        {m}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}

            {selected.models.length === 0 && (
              <div className="space-y-1.5">
                <Label htmlFor="prov-model-custom" className="text-xs">
                  Default Model
                </Label>
                <Input
                  id="prov-model-custom"
                  placeholder="Enter model name"
                  value={selectedModel}
                  onChange={(e) => setSelectedModel(e.target.value)}
                />
              </div>
            )}

            {error && (
              <p className="text-xs text-destructive">{error}</p>
            )}

            <div className="flex justify-end gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setSelected(null)
                  setFormValues({})
                  setError(null)
                }}
              >
                Cancel
              </Button>
              <Button size="sm" onClick={handleSave} disabled={saving}>
                {saving ? "Saving..." : "Save Provider"}
              </Button>
            </div>
          </div>
        )}

        {/* Configured providers list */}
        {configuredCount > 0 && (
          <div className="space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">
              Configured providers:
            </h3>
            {providers.map((p: Provider) => {
              const result = testResults[p.name]
              return (
                <div
                  key={p.name}
                  className="flex items-center justify-between rounded-lg border px-3 py-2"
                >
                  <div className="flex items-center gap-2 text-sm">
                    {result?.success ? (
                      <span className="text-green-500">&#10003;</span>
                    ) : result && !result.success ? (
                      <span className="text-destructive">&#10007;</span>
                    ) : (
                      <span className="text-muted-foreground">&#8226;</span>
                    )}
                    <span className="font-medium">{p.name}</span>
                    <span className="text-muted-foreground text-xs">
                      ({p.default_model})
                    </span>
                    {result?.success && result.latency_ms && (
                      <span className="text-xs text-muted-foreground">
                        {result.latency_ms}ms
                      </span>
                    )}
                    {result && !result.success && result.error && (
                      <span className="text-xs text-destructive">
                        {result.error}
                      </span>
                    )}
                  </div>
                  <div className="flex gap-1">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => handleTest(p.name)}
                      disabled={testing === p.name}
                    >
                      {testing === p.name ? "Testing..." : "Test Connection"}
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleRemove(p.name)}
                      className="text-destructive hover:text-destructive"
                    >
                      Remove
                    </Button>
                  </div>
                </div>
              )
            })}
          </div>
        )}

        {configuredCount === 0 && (
          <p className="text-xs text-muted-foreground text-center">
            No providers configured yet. Select a provider above to get started.
          </p>
        )}
      </CardContent>
    </Card>
  )
}

/** Check if at least one provider is configured. */
export function useHasProvider(): boolean {
  return useProviderStore((s) => s.providers.length > 0)
}
