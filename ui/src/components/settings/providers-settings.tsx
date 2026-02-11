import { useEffect, useState } from "react"
import { Card, CardContent } from "@/components/ui/card"
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { providerDefs, type ProviderDef } from "@/lib/provider-defs"
import { useProviderStore } from "@/stores/providers"
import type { Provider } from "@/api/types"

interface FormValues {
  [key: string]: string
}

export function ProvidersSettings() {
  const providers = useProviderStore((s) => s.providers)
  const testResults = useProviderStore((s) => s.testResults)
  const fetchProviders = useProviderStore((s) => s.fetchProviders)
  const createProvider = useProviderStore((s) => s.createProvider)
  const deleteProvider = useProviderStore((s) => s.deleteProvider)
  const testProvider = useProviderStore((s) => s.testProvider)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [selected, setSelected] = useState<ProviderDef | null>(null)
  const [formValues, setFormValues] = useState<FormValues>({})
  const [selectedModel, setSelectedModel] = useState("")
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchProviders()
  }, [fetchProviders])

  const handleSelectProvider = (def: ProviderDef) => {
    setSelected(def)
    setError(null)
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
      setDialogOpen(false)
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
      // result stored in store
    } finally {
      setTesting(null)
    }
  }

  const handleRemove = async (name: string) => {
    await deleteProvider(name)
  }

  const isConfigured = (id: string) => providers.some((p: Provider) => p.name === id)

  return (
    <div className="space-y-6 max-w-2xl">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-medium">LLM Providers</h2>
          <p className="text-sm text-muted-foreground">
            Manage your LLM provider connections.
          </p>
        </div>
        <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
          <DialogTrigger asChild>
            <Button size="sm">+ Add Provider</Button>
          </DialogTrigger>
          <DialogContent className="max-w-md">
            <DialogHeader>
              <DialogTitle>Add Provider</DialogTitle>
              <DialogDescription>
                Select a provider and enter your credentials.
              </DialogDescription>
            </DialogHeader>

            {/* Provider selector */}
            {!selected && (
              <div className="grid grid-cols-3 gap-2">
                {providerDefs
                  .filter((def) => !isConfigured(def.id))
                  .map((def) => (
                    <button
                      key={def.id}
                      type="button"
                      onClick={() => handleSelectProvider(def)}
                      className="flex flex-col items-center gap-1 rounded-lg border p-3 text-center text-xs hover:border-primary/50 hover:bg-muted/50 cursor-pointer transition-colors"
                    >
                      <span className="font-medium text-foreground">
                        {def.label}
                      </span>
                      <span className="text-muted-foreground">
                        {def.description}
                      </span>
                    </button>
                  ))}
              </div>
            )}

            {/* Configuration form */}
            {selected && (
              <div className="space-y-4">
                <div className="flex items-center gap-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setSelected(null)
                      setFormValues({})
                      setError(null)
                    }}
                  >
                    &larr;
                  </Button>
                  <span className="text-sm font-medium">{selected.label}</span>
                </div>

                {selected.fields.map((field) => (
                  <div key={field.name} className="space-y-1.5">
                    <Label htmlFor={`sp-${field.name}`} className="text-xs">
                      {field.label}
                      {field.required && (
                        <span className="text-destructive ml-0.5">*</span>
                      )}
                    </Label>
                    <Input
                      id={`sp-${field.name}`}
                      type={field.type}
                      placeholder={field.placeholder}
                      value={formValues[field.name] ?? ""}
                      onChange={(e) =>
                        handleFieldChange(field.name, e.target.value)
                      }
                    />
                  </div>
                ))}

                {selected.models.length > 0 ? (
                  <div className="space-y-1.5">
                    <Label htmlFor="sp-model" className="text-xs">
                      Default Model
                      <span className="text-destructive ml-0.5">*</span>
                    </Label>
                    <Select
                      value={selectedModel}
                      onValueChange={setSelectedModel}
                    >
                      <SelectTrigger id="sp-model">
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
                ) : (
                  <div className="space-y-1.5">
                    <Label htmlFor="sp-model-custom" className="text-xs">
                      Default Model
                    </Label>
                    <Input
                      id="sp-model-custom"
                      placeholder="Enter model name"
                      value={selectedModel}
                      onChange={(e) => setSelectedModel(e.target.value)}
                    />
                  </div>
                )}

                {error && <p className="text-xs text-destructive">{error}</p>}

                <div className="flex justify-end gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setDialogOpen(false)
                      setSelected(null)
                      setFormValues({})
                      setError(null)
                    }}
                  >
                    Cancel
                  </Button>
                  <Button size="sm" onClick={handleSave} disabled={saving}>
                    {saving ? "Saving..." : "Save"}
                  </Button>
                </div>
              </div>
            )}
          </DialogContent>
        </Dialog>
      </div>

      {/* Provider list */}
      {providers.length === 0 ? (
        <Card>
          <CardContent className="py-8 text-center text-sm text-muted-foreground">
            No providers configured. Click "+ Add Provider" to get started.
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-2">
          {providers.map((p: Provider) => {
            const result = testResults[p.name]
            const def = providerDefs.find((d) => d.id === p.name)
            return (
              <Card key={p.name}>
                <CardContent className="flex items-center justify-between py-3">
                  <div className="flex items-center gap-3">
                    <div>
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium">
                          {def?.label ?? p.name}
                        </span>
                        {p.verified ? (
                          <Badge
                            variant="secondary"
                            className="bg-green-500/10 text-green-600 text-[10px]"
                          >
                            Verified
                          </Badge>
                        ) : (
                          <Badge
                            variant="secondary"
                            className="text-[10px]"
                          >
                            Unverified
                          </Badge>
                        )}
                      </div>
                      <p className="text-xs text-muted-foreground">
                        {p.default_model}
                        {result?.success && result.latency_ms
                          ? ` \u00b7 ${result.latency_ms}ms`
                          : ""}
                      </p>
                      {result && !result.success && result.error && (
                        <p className="text-xs text-destructive mt-0.5">
                          {result.error}
                        </p>
                      )}
                    </div>
                  </div>
                  <div className="flex gap-1">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => handleTest(p.name)}
                      disabled={testing === p.name}
                    >
                      {testing === p.name ? "Testing..." : "Test"}
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
                </CardContent>
              </Card>
            )
          })}
        </div>
      )}
    </div>
  )
}
