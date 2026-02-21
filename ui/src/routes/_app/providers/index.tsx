import { useEffect, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useProviderStore, PROVIDER_NAMES, DEFAULT_MODELS } from '@/stores/provider'
import { Icon } from '@/components/ui/icon'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { Provider, ProviderType } from '@/lib/api-types'

export const Route = createFileRoute('/_app/providers/')({
  component: ProvidersPage,
})

// Provider logos/icons represented by color
const PROVIDER_COLORS: Record<ProviderType, string> = {
  anthropic: 'var(--orange)',
  openai: 'var(--green)',
  google: 'var(--blue)',
  ollama: 'var(--purple)',
}

function ProvidersPage() {
  const rawProviders = useProviderStore((s) => s.providers)
  const loading = useProviderStore((s) => s.loading)
  const fetchProviders = useProviderStore((s) => s.fetchProviders)
  const deleteProvider = useProviderStore((s) => s.deleteProvider)
  const testProvider = useProviderStore((s) => s.testProvider)

  // Defensive: ensure providers is always an array
  const providers = Array.isArray(rawProviders) ? rawProviders : []

  const [selectedProvider, setSelectedProvider] = useState<Provider | null>(null)
  const [showAddModal, setShowAddModal] = useState(false)
  const [testingId, setTestingId] = useState<string | null>(null)

  useEffect(() => {
    fetchProviders()
  }, [fetchProviders])

  const handleTest = async (provider: Provider) => {
    setTestingId(provider.id)
    try {
      const result = await testProvider(provider.id)
      if (result.success) {
        alert(`Connection successful! Available models: ${result.models?.slice(0, 5).join(', ')}${result.models && result.models.length > 5 ? '...' : ''}`)
      } else {
        alert('Connection test failed')
      }
    } catch {
      alert('Connection test failed')
    } finally {
      setTestingId(null)
    }
  }

  const handleDelete = async (provider: Provider) => {
    if (confirm(`Delete provider "${provider.name}"?`)) {
      await deleteProvider(provider.id)
      if (selectedProvider?.id === provider.id) {
        setSelectedProvider(null)
      }
    }
  }

  return (
    <div className="flex h-full overflow-hidden">
      {/* Main content */}
      <div className="flex-1 overflow-auto">
        <div className="p-7">
          <div className="flex items-center justify-between mb-6">
            <div>
              <h1 className="text-xl font-bold text-foreground">LLM Providers</h1>
              <p className="text-sm text-muted-foreground mt-1">
                Configure API connections to LLM providers
              </p>
            </div>
            <button
              onClick={() => setShowAddModal(true)}
              className={cn(
                'inline-flex items-center gap-1.5 rounded-lg font-semibold transition-all',
                'text-[13px] px-3.5 py-2',
                'bg-primary text-white hover:bg-primary/90'
              )}
            >
              <Icon name="plus" size={15} />
              Add Provider
            </button>
          </div>

          {loading ? (
            <div className="flex items-center justify-center h-64">
              <div className="animate-pulse text-sm text-muted-foreground">Loading providers...</div>
            </div>
          ) : providers.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-64 text-center">
              <div className="w-14 h-14 mb-4 rounded-full bg-surface-2 flex items-center justify-center">
                <Icon name="providers" size={24} className="text-muted-foreground" />
              </div>
              <h3 className="text-lg font-semibold text-foreground mb-1">No providers configured</h3>
              <p className="text-sm text-muted-foreground max-w-sm">
                Add an LLM provider to start building workflows. Supports Anthropic, OpenAI, Google, and Ollama.
              </p>
            </div>
          ) : (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              {providers.map((provider) => (
                <ProviderCard
                  key={provider.id}
                  provider={provider}
                  selected={selectedProvider?.id === provider.id}
                  testing={testingId === provider.id}
                  onSelect={() => setSelectedProvider(provider)}
                  onTest={() => handleTest(provider)}
                  onDelete={() => handleDelete(provider)}
                />
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Detail panel */}
      {selectedProvider && (
        <div className="w-[380px] border-l border-border bg-surface-0 overflow-auto">
          <ProviderDetail provider={selectedProvider} onClose={() => setSelectedProvider(null)} />
        </div>
      )}

      {/* Add modal */}
      {showAddModal && (
        <AddProviderModal onClose={() => setShowAddModal(false)} />
      )}
    </div>
  )
}

interface ProviderCardProps {
  provider: Provider
  selected: boolean
  testing: boolean
  onSelect: () => void
  onTest: () => void
  onDelete: () => void
}

function ProviderCard({ provider, selected, testing, onSelect, onTest, onDelete }: ProviderCardProps) {
  return (
    <div
      onClick={onSelect}
      className={cn(
        'p-4 rounded-xl border bg-surface-0 cursor-pointer transition-all',
        selected ? 'border-primary ring-1 ring-primary' : 'border-border hover:border-primary/50'
      )}
    >
      <div className="flex items-start justify-between mb-3">
        <div className="flex items-center gap-3">
          <div
            className="w-10 h-10 rounded-lg flex items-center justify-center"
            style={{ backgroundColor: `${PROVIDER_COLORS[provider.type]}20` }}
          >
            <div
              className="w-5 h-5 rounded-full"
              style={{ backgroundColor: PROVIDER_COLORS[provider.type] }}
            />
          </div>
          <div>
            <div className="font-semibold text-sm text-foreground">{provider.name}</div>
            <div className="text-xs text-muted-foreground">{PROVIDER_NAMES[provider.type]}</div>
          </div>
        </div>
        <StatusBadge status={provider.status === 'connected' ? 'connected' : 'disconnected'} />
      </div>

      {provider.default_model && (
        <div className="flex items-center gap-2 mb-3">
          <span className="text-xs text-muted-foreground">Default model:</span>
          <Badge variant="default">{provider.default_model}</Badge>
        </div>
      )}

      <div className="flex items-center justify-between">
        <div className="text-xs text-muted-foreground">
          Added {new Date(provider.created_at).toLocaleDateString()}
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={(e) => {
              e.stopPropagation()
              onTest()
            }}
            disabled={testing}
            className={cn(
              'p-1.5 rounded-lg hover:bg-surface-1 text-muted-foreground hover:text-foreground transition-colors',
              testing && 'opacity-50'
            )}
            title="Test connection"
          >
            <Icon name={testing ? 'clock' : 'zap'} size={14} />
          </button>
          <button
            onClick={(e) => {
              e.stopPropagation()
              onDelete()
            }}
            className="p-1.5 rounded-lg hover:bg-surface-1 text-muted-foreground hover:text-red transition-colors"
            title="Delete"
          >
            <Icon name="trash" size={14} />
          </button>
        </div>
      </div>
    </div>
  )
}

interface ProviderDetailProps {
  provider: Provider
  onClose: () => void
}

function ProviderDetail({ provider, onClose }: ProviderDetailProps) {
  const models = DEFAULT_MODELS[provider.type]

  return (
    <div className="p-4">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-sm font-bold text-foreground">Provider Details</h2>
        <button
          onClick={onClose}
          className="p-1 text-muted-foreground hover:text-foreground transition-colors"
        >
          <Icon name="x" size={16} />
        </button>
      </div>

      <div className="space-y-4">
        {/* Header */}
        <div className="p-3 rounded-lg bg-surface-1 border border-border">
          <div className="flex items-center gap-3 mb-2">
            <div
              className="w-10 h-10 rounded-lg flex items-center justify-center"
              style={{ backgroundColor: `${PROVIDER_COLORS[provider.type]}20` }}
            >
              <div
                className="w-5 h-5 rounded-full"
                style={{ backgroundColor: PROVIDER_COLORS[provider.type] }}
              />
            </div>
            <div>
              <div className="font-bold text-sm text-foreground">{provider.name}</div>
              <div className="text-xs text-muted-foreground">{PROVIDER_NAMES[provider.type]}</div>
            </div>
          </div>
          <StatusBadge status={provider.status === 'connected' ? 'connected' : 'disconnected'} />
        </div>

        {/* Configuration */}
        <div>
          <div className="text-xs font-semibold text-muted-foreground uppercase mb-2">
            Configuration
          </div>
          <div className="space-y-2 text-xs">
            <div className="flex justify-between">
              <span className="text-muted-foreground">Provider ID</span>
              <span className="text-foreground font-mono">{provider.id.slice(0, 8)}...</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Type</span>
              <span className="text-foreground">{PROVIDER_NAMES[provider.type]}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Default Model</span>
              <span className="text-foreground">{provider.default_model || 'Not set'}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Added</span>
              <span className="text-foreground">
                {new Date(provider.created_at).toLocaleDateString()}
              </span>
            </div>
          </div>
        </div>

        {/* Available models */}
        <div>
          <div className="text-xs font-semibold text-muted-foreground uppercase mb-2">
            Available Models
          </div>
          <div className="space-y-1.5">
            {models.map((model) => (
              <div
                key={model}
                className={cn(
                  'px-3 py-2 rounded-lg border text-xs',
                  model === provider.default_model
                    ? 'bg-accent-soft border-primary text-primary'
                    : 'bg-surface-1 border-border text-foreground'
                )}
              >
                {model}
                {model === provider.default_model && (
                  <span className="ml-2 text-[10px] opacity-70">(default)</span>
                )}
              </div>
            ))}
          </div>
        </div>

        {/* API Key status */}
        <div>
          <div className="text-xs font-semibold text-muted-foreground uppercase mb-2">
            API Key
          </div>
          <div className="p-3 rounded-lg bg-surface-1 border border-border">
            <div className="flex items-center gap-2">
              <Icon name="check" size={14} className="text-green" />
              <span className="text-xs text-foreground">API key configured</span>
            </div>
            <p className="text-[11px] text-muted-foreground mt-1">
              Key is securely stored and not displayed
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}

interface AddProviderModalProps {
  onClose: () => void
}

function AddProviderModal({ onClose }: AddProviderModalProps) {
  const addProvider = useProviderStore((s) => s.addProvider)
  const [type, setType] = useState<ProviderType>('anthropic')
  const [name, setName] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [defaultModel, setDefaultModel] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Update default model when type changes
  useEffect(() => {
    setDefaultModel(DEFAULT_MODELS[type][0])
  }, [type])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setIsSubmitting(true)
    setError(null)

    try {
      await addProvider({
        type,
        name: name || PROVIDER_NAMES[type],
        default_model: defaultModel,
        api_key: apiKey,
      })
      onClose()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onClose}
    >
      <div
        className="bg-surface-0 rounded-xl shadow-lg border border-border p-6 w-full max-w-md"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex justify-between items-center mb-4">
          <h2 className="text-lg font-bold text-foreground">Add Provider</h2>
          <button
            onClick={onClose}
            className="p-1 text-muted-foreground hover:text-foreground transition-colors"
          >
            <Icon name="x" size={18} />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          {/* Provider type */}
          <div>
            <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
              Provider
            </label>
            <div className="grid grid-cols-2 gap-2">
              {(Object.keys(PROVIDER_NAMES) as ProviderType[]).map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => setType(t)}
                  className={cn(
                    'p-3 rounded-lg border text-sm font-medium transition-all flex items-center gap-2',
                    type === t
                      ? 'border-primary bg-accent-soft'
                      : 'border-border bg-surface-1 hover:border-primary/50'
                  )}
                >
                  <div
                    className="w-4 h-4 rounded-full"
                    style={{ backgroundColor: PROVIDER_COLORS[t] }}
                  />
                  <span className={type === t ? 'text-primary' : 'text-foreground'}>
                    {PROVIDER_NAMES[t]}
                  </span>
                </button>
              ))}
            </div>
          </div>

          {/* Name */}
          <div>
            <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
              Display Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={PROVIDER_NAMES[type]}
              className={cn(
                'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                'text-foreground text-sm',
                'focus:outline-none focus:ring-1 focus:ring-primary'
              )}
            />
          </div>

          {/* API Key */}
          <div>
            <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
              API Key
            </label>
            <input
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder={type === 'ollama' ? 'Not required for local Ollama' : 'sk-...'}
              required={type !== 'ollama'}
              className={cn(
                'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                'text-foreground text-sm',
                'focus:outline-none focus:ring-1 focus:ring-primary'
              )}
            />
            {type === 'ollama' && (
              <p className="text-[11px] text-muted-foreground mt-1">
                API key is optional for local Ollama instances
              </p>
            )}
          </div>

          {/* Default model */}
          <div>
            <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
              Default Model
            </label>
            <select
              value={defaultModel}
              onChange={(e) => setDefaultModel(e.target.value)}
              className={cn(
                'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                'text-foreground text-sm',
                'focus:outline-none focus:ring-1 focus:ring-primary'
              )}
            >
              {DEFAULT_MODELS[type].map((model) => (
                <option key={model} value={model}>
                  {model}
                </option>
              ))}
            </select>
          </div>

          {error && (
            <div className="p-3 rounded-lg bg-red-soft text-red text-sm">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className={cn(
                'px-4 py-2 rounded-lg font-semibold text-sm transition-colors',
                'bg-transparent text-muted-foreground hover:text-foreground'
              )}
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isSubmitting || (!apiKey && type !== 'ollama')}
              className={cn(
                'inline-flex items-center gap-2 px-4 py-2 rounded-lg font-semibold text-sm transition-all',
                'bg-primary text-white hover:bg-primary/90',
                (isSubmitting || (!apiKey && type !== 'ollama')) && 'opacity-50 cursor-not-allowed'
              )}
            >
              {isSubmitting ? 'Adding...' : 'Add Provider'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
