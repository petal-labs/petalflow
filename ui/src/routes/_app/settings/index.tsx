import { useEffect } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useSettingsStore } from '@/stores/settings'
import { useProviderStore, PROVIDER_NAMES, DEFAULT_MODELS } from '@/stores/provider'
import { Icon } from '@/components/ui/icon'
import { cn } from '@/lib/utils'

export const Route = createFileRoute('/_app/settings/')({
  component: SettingsPage,
})

function SettingsPage() {
  const theme = useSettingsStore((s) => s.theme)
  const setTheme = useSettingsStore((s) => s.setTheme)
  const defaultProvider = useSettingsStore((s) => s.defaultProvider)
  const setDefaultProvider = useSettingsStore((s) => s.setDefaultProvider)
  const defaultModel = useSettingsStore((s) => s.defaultModel)
  const setDefaultModel = useSettingsStore((s) => s.setDefaultModel)
  const editor = useSettingsStore((s) => s.editor)
  const updateEditorPreference = useSettingsStore((s) => s.updateEditorPreference)
  const run = useSettingsStore((s) => s.run)
  const updateRunPreference = useSettingsStore((s) => s.updateRunPreference)

  const rawProviders = useProviderStore((s) => s.providers)
  const fetchProviders = useProviderStore((s) => s.fetchProviders)

  // Defensive: ensure providers is always an array
  const providers = Array.isArray(rawProviders) ? rawProviders : []

  useEffect(() => {
    fetchProviders()
  }, [fetchProviders])

  // Get models for selected provider
  const selectedProviderType = providers.find((p) => p.id === defaultProvider)?.type
  const availableModels = selectedProviderType ? DEFAULT_MODELS[selectedProviderType] : []

  return (
    <div className="p-7 max-w-3xl">
      <div className="mb-8">
        <h1 className="text-xl font-bold text-foreground">Settings</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Configure your PetalFlow workspace preferences
        </p>
      </div>

      <div className="space-y-8">
        {/* Appearance */}
        <section>
          <h2 className="text-sm font-bold text-foreground mb-4 flex items-center gap-2">
            <Icon name="sun" size={16} />
            Appearance
          </h2>
          <div className="p-4 rounded-xl bg-surface-0 border border-border space-y-4">
            <div>
              <label className="block text-xs font-semibold text-muted-foreground mb-2">
                Theme
              </label>
              <div className="flex gap-2">
                {(['light', 'dark', 'system'] as const).map((t) => (
                  <button
                    key={t}
                    onClick={() => setTheme(t)}
                    className={cn(
                      'flex-1 p-3 rounded-lg border text-sm font-medium transition-all',
                      theme === t
                        ? 'border-primary bg-accent-soft text-primary'
                        : 'border-border bg-surface-1 text-muted-foreground hover:border-primary/50'
                    )}
                  >
                    <Icon
                      name={t === 'light' ? 'sun' : t === 'dark' ? 'moon' : 'settings'}
                      size={16}
                      className="mx-auto mb-1"
                    />
                    {t.charAt(0).toUpperCase() + t.slice(1)}
                  </button>
                ))}
              </div>
            </div>
          </div>
        </section>

        {/* Default Provider & Model */}
        <section>
          <h2 className="text-sm font-bold text-foreground mb-4 flex items-center gap-2">
            <Icon name="providers" size={16} />
            Default LLM
          </h2>
          <div className="p-4 rounded-xl bg-surface-0 border border-border space-y-4">
            <div>
              <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
                Default Provider
              </label>
              <select
                value={defaultProvider || ''}
                onChange={(e) => {
                  setDefaultProvider(e.target.value || null)
                  setDefaultModel(null)
                }}
                className={cn(
                  'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                  'text-foreground text-sm',
                  'focus:outline-none focus:ring-1 focus:ring-primary'
                )}
              >
                <option value="">Select a provider</option>
                {providers.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name} ({PROVIDER_NAMES[p.type]})
                  </option>
                ))}
              </select>
              {providers.length === 0 && (
                <p className="text-[11px] text-muted-foreground mt-1">
                  No providers configured. Add one in the Providers page.
                </p>
              )}
            </div>

            <div>
              <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
                Default Model
              </label>
              <select
                value={defaultModel || ''}
                onChange={(e) => setDefaultModel(e.target.value || null)}
                disabled={!defaultProvider}
                className={cn(
                  'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                  'text-foreground text-sm',
                  'focus:outline-none focus:ring-1 focus:ring-primary',
                  !defaultProvider && 'opacity-50 cursor-not-allowed'
                )}
              >
                <option value="">Select a model</option>
                {availableModels.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </div>
          </div>
        </section>

        {/* Editor Preferences */}
        <section>
          <h2 className="text-sm font-bold text-foreground mb-4 flex items-center gap-2">
            <Icon name="designer" size={16} />
            Editor
          </h2>
          <div className="p-4 rounded-xl bg-surface-0 border border-border space-y-4">
            <ToggleSetting
              label="Auto-layout on save"
              description="Automatically arrange nodes when saving a workflow"
              checked={editor.autoLayoutOnSave}
              onChange={(v) => updateEditorPreference('autoLayoutOnSave', v)}
            />
            <ToggleSetting
              label="Show port types"
              description="Display data types on node connection ports"
              checked={editor.showPortTypes}
              onChange={(v) => updateEditorPreference('showPortTypes', v)}
            />
            <ToggleSetting
              label="Confirm before delete"
              description="Show confirmation dialog when deleting nodes"
              checked={editor.confirmBeforeDelete}
              onChange={(v) => updateEditorPreference('confirmBeforeDelete', v)}
            />
          </div>
        </section>

        {/* Run Preferences */}
        <section>
          <h2 className="text-sm font-bold text-foreground mb-4 flex items-center gap-2">
            <Icon name="play" size={16} />
            Execution
          </h2>
          <div className="p-4 rounded-xl bg-surface-0 border border-border space-y-4">
            <ToggleSetting
              label="Streaming enabled"
              description="Show incremental output as it arrives during runs"
              checked={run.streamingEnabled}
              onChange={(v) => updateRunPreference('streamingEnabled', v)}
            />
            <ToggleSetting
              label="Tracing enabled"
              description="Enable OpenTelemetry trace collection for runs"
              checked={run.tracingEnabled}
              onChange={(v) => updateRunPreference('tracingEnabled', v)}
            />
            <div>
              <div className="flex items-center justify-between mb-1.5">
                <div>
                  <div className="text-sm font-medium text-foreground">Default concurrency</div>
                  <div className="text-[11px] text-muted-foreground">
                    Maximum parallel tasks during execution
                  </div>
                </div>
                <span className="text-sm font-mono text-foreground bg-surface-1 px-2 py-1 rounded">
                  {run.defaultConcurrency}
                </span>
              </div>
              <input
                type="range"
                min={1}
                max={16}
                value={run.defaultConcurrency}
                onChange={(e) => updateRunPreference('defaultConcurrency', parseInt(e.target.value))}
                className="w-full accent-primary"
              />
              <div className="flex justify-between text-[10px] text-muted-foreground mt-1">
                <span>1</span>
                <span>16</span>
              </div>
            </div>
          </div>
        </section>

        {/* About */}
        <section>
          <h2 className="text-sm font-bold text-foreground mb-4 flex items-center gap-2">
            <Icon name="zap" size={16} />
            About
          </h2>
          <div className="p-4 rounded-xl bg-surface-0 border border-border">
            <div className="flex items-center gap-3 mb-4">
              <div className="w-12 h-12 rounded-xl bg-gradient-to-br from-primary to-teal flex items-center justify-center">
                <span className="text-white font-bold text-lg">PF</span>
              </div>
              <div>
                <div className="font-bold text-foreground">PetalFlow</div>
                <div className="text-xs text-muted-foreground">AI Workflow Designer</div>
              </div>
            </div>
            <div className="space-y-2 text-xs">
              <div className="flex justify-between">
                <span className="text-muted-foreground">Version</span>
                <span className="text-foreground">0.1.0-alpha</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Build</span>
                <span className="text-foreground font-mono">
                  {new Date().toISOString().slice(0, 10).replace(/-/g, '')}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Runtime</span>
                <span className="text-foreground">Go + React</span>
              </div>
            </div>
            <div className="mt-4 pt-4 border-t border-border">
              <a
                href="https://github.com/petal-labs/petalflow"
                target="_blank"
                rel="noopener noreferrer"
                className={cn(
                  'inline-flex items-center gap-2 px-3 py-2 rounded-lg text-xs font-medium',
                  'bg-surface-1 text-muted-foreground hover:text-foreground transition-colors'
                )}
              >
                <Icon name="git" size={14} />
                View on GitHub
              </a>
            </div>
          </div>
        </section>
      </div>
    </div>
  )
}

interface ToggleSettingProps {
  label: string
  description: string
  checked: boolean
  onChange: (checked: boolean) => void
}

function ToggleSetting({ label, description, checked, onChange }: ToggleSettingProps) {
  return (
    <label className="flex items-start gap-3 cursor-pointer">
      <div className="pt-0.5">
        <input
          type="checkbox"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
          className="sr-only peer"
        />
        <div
          className={cn(
            'w-9 h-5 rounded-full transition-colors relative',
            checked ? 'bg-primary' : 'bg-muted'
          )}
        >
          <div
            className={cn(
              'absolute top-0.5 w-4 h-4 rounded-full bg-white transition-transform',
              checked ? 'translate-x-4' : 'translate-x-0.5'
            )}
          />
        </div>
      </div>
      <div className="flex-1">
        <div className="text-sm font-medium text-foreground">{label}</div>
        <div className="text-[11px] text-muted-foreground">{description}</div>
      </div>
    </label>
  )
}
