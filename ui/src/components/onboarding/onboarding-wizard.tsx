import { useState } from 'react'
import { useSettingsStore } from '@/stores/settings'
import { useProviderStore, PROVIDER_NAMES, DEFAULT_MODELS } from '@/stores/provider'
import { useWorkflowStore } from '@/stores/workflow'
import { Icon, type IconName } from '@/components/ui/icon'
import { cn } from '@/lib/utils'
import type { ProviderType } from '@/lib/api-types'

type Step = 'welcome' | 'provider' | 'demo' | 'complete'

const STEPS: { key: Step; label: string }[] = [
  { key: 'welcome', label: 'Welcome' },
  { key: 'provider', label: 'Provider' },
  { key: 'demo', label: 'Demo Workflows' },
  { key: 'complete', label: 'Complete' },
]

interface ProviderConfig {
  type: ProviderType
  name: string
  apiKey: string
  model: string
}

export function OnboardingWizard() {
  const completeOnboarding = useSettingsStore((s) => s.completeOnboarding)
  const skipOnboarding = useSettingsStore((s) => s.skipOnboarding)
  const setDefaultProvider = useSettingsStore((s) => s.setDefaultProvider)
  const setDefaultModel = useSettingsStore((s) => s.setDefaultModel)
  const addProvider = useProviderStore((s) => s.addProvider)
  const createAgentWorkflow = useWorkflowStore((s) => s.createAgentWorkflow)
  const createGraphWorkflow = useWorkflowStore((s) => s.createGraphWorkflow)

  const [currentStep, setCurrentStep] = useState<Step>('welcome')
  const [providerConfig, setProviderConfig] = useState<ProviderConfig>({
    type: 'anthropic',
    name: 'My Anthropic',
    apiKey: '',
    model: 'claude-sonnet-4-20250514',
  })
  const [createDemos, setCreateDemos] = useState({
    simple: true,
    agent: false,
  })
  const [loading, setLoading] = useState(false)

  const currentStepIndex = STEPS.findIndex((s) => s.key === currentStep)

  const handleProviderTypeChange = (type: ProviderType) => {
    setProviderConfig({
      type,
      name: `My ${PROVIDER_NAMES[type]}`,
      apiKey: '',
      model: DEFAULT_MODELS[type][0],
    })
  }

  const handleNext = async () => {
    if (currentStep === 'welcome') {
      setCurrentStep('provider')
    } else if (currentStep === 'provider') {
      setCurrentStep('demo')
    } else if (currentStep === 'demo') {
      setLoading(true)
      try {
        // Create provider if API key provided
        if (providerConfig.apiKey.trim()) {
          const provider = await addProvider({
            type: providerConfig.type,
            name: providerConfig.name,
            status: 'connected',
            default_model: providerConfig.model,
            api_key: providerConfig.apiKey,
          })
          setDefaultProvider(provider.id)
          setDefaultModel(providerConfig.model)
        }

        // Create demo workflows if selected
        if (createDemos.simple) {
          await createGraphWorkflow({
            id: crypto.randomUUID(),
            version: '1.0.0',
            nodes: [
              {
                id: 'input',
                type: 'input',
                config: { schema: { type: 'object', properties: { prompt: { type: 'string' } } } },
              },
              {
                id: 'llm',
                type: 'llm',
                config: { provider: '', model: '', system: 'You are a helpful assistant.' },
              },
              {
                id: 'output',
                type: 'output',
                config: {},
              },
            ],
            edges: [
              { source: 'input', source_handle: 'out', target: 'llm', target_handle: 'prompt' },
              { source: 'llm', source_handle: 'response', target: 'output', target_handle: 'in' },
            ],
            entry: 'input',
          })
        }
        if (createDemos.agent) {
          await createAgentWorkflow({
            version: '1.0.0',
            kind: 'agent_workflow',
            id: crypto.randomUUID(),
            name: 'Research Assistant',
            agents: {
              researcher: {
                id: 'researcher',
                role: 'Research Assistant',
                goal: 'Help users research topics and answer questions',
                provider: '',
                model: '',
              },
            },
            tasks: {
              research: {
                id: 'research',
                description: 'Research the given topic and provide a comprehensive answer',
                agent: 'researcher',
                expected_output: 'A detailed answer to the research question',
              },
            },
            execution: {
              strategy: 'sequential',
              task_order: ['research'],
            },
          })
        }
      } catch {
        // Continue even if creation fails - user can set up manually
      }
      setLoading(false)
      setCurrentStep('complete')
    } else if (currentStep === 'complete') {
      completeOnboarding()
    }
  }

  const handleBack = () => {
    if (currentStep === 'provider') {
      setCurrentStep('welcome')
    } else if (currentStep === 'demo') {
      setCurrentStep('provider')
    }
  }

  const handleSkip = () => {
    skipOnboarding()
  }

  return (
    <div className="fixed inset-0 bg-background z-50 flex items-center justify-center">
      <div className="w-full max-w-2xl mx-4">
        {/* Progress indicator */}
        <div className="flex items-center justify-center mb-8 gap-2">
          {STEPS.map((step, idx) => (
            <div key={step.key} className="flex items-center gap-2">
              <div
                className={cn(
                  'w-8 h-8 rounded-full flex items-center justify-center text-sm font-medium transition-colors',
                  idx < currentStepIndex
                    ? 'bg-primary text-primary-foreground'
                    : idx === currentStepIndex
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground'
                )}
              >
                {idx < currentStepIndex ? (
                  <Icon name="check" size={16} />
                ) : (
                  idx + 1
                )}
              </div>
              {idx < STEPS.length - 1 && (
                <div
                  className={cn(
                    'w-12 h-0.5',
                    idx < currentStepIndex ? 'bg-primary' : 'bg-muted'
                  )}
                />
              )}
            </div>
          ))}
        </div>

        {/* Card */}
        <div className="bg-surface-0 border border-border rounded-2xl p-8">
          {/* Welcome Step */}
          {currentStep === 'welcome' && (
            <div className="text-center">
              <div className="w-20 h-20 mx-auto mb-6 rounded-2xl bg-gradient-to-br from-primary to-teal flex items-center justify-center">
                <span className="text-white font-bold text-3xl">PF</span>
              </div>
              <h1 className="text-2xl font-bold text-foreground mb-2">
                Welcome to PetalFlow
              </h1>
              <p className="text-muted-foreground mb-8 max-w-md mx-auto">
                Build, orchestrate, and deploy AI agents and workflows with a powerful visual designer and Go-native runtime.
              </p>
              <div className="grid grid-cols-3 gap-4 text-left mb-8">
                <FeatureCard
                  icon="workflows"
                  title="Visual Designer"
                  description="Drag-and-drop workflow editor"
                />
                <FeatureCard
                  icon="providers"
                  title="Multi-Provider"
                  description="OpenAI, Anthropic, Google, Ollama"
                />
                <FeatureCard
                  icon="play"
                  title="Real-time Runs"
                  description="Stream execution with live events"
                />
              </div>
            </div>
          )}

          {/* Provider Step */}
          {currentStep === 'provider' && (
            <div>
              <h2 className="text-xl font-bold text-foreground mb-2">
                Configure Your First Provider
              </h2>
              <p className="text-muted-foreground mb-6">
                Connect an LLM provider to power your workflows. You can add more providers later.
              </p>

              <div className="space-y-4">
                <div>
                  <label className="block text-sm font-medium text-foreground mb-2">
                    Provider Type
                  </label>
                  <div className="grid grid-cols-4 gap-2">
                    {(Object.keys(PROVIDER_NAMES) as ProviderType[]).map((type) => (
                      <button
                        key={type}
                        onClick={() => handleProviderTypeChange(type)}
                        className={cn(
                          'p-3 rounded-lg border text-sm font-medium transition-all',
                          providerConfig.type === type
                            ? 'border-primary bg-accent-soft text-primary'
                            : 'border-border bg-surface-1 text-muted-foreground hover:border-primary/50'
                        )}
                      >
                        {PROVIDER_NAMES[type]}
                      </button>
                    ))}
                  </div>
                </div>

                <div>
                  <label className="block text-sm font-medium text-foreground mb-2">
                    Name
                  </label>
                  <input
                    type="text"
                    value={providerConfig.name}
                    onChange={(e) =>
                      setProviderConfig({ ...providerConfig, name: e.target.value })
                    }
                    className={cn(
                      'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                      'text-foreground text-sm',
                      'focus:outline-none focus:ring-1 focus:ring-primary'
                    )}
                    placeholder="e.g., Production Anthropic"
                  />
                </div>

                <div>
                  <label className="block text-sm font-medium text-foreground mb-2">
                    API Key
                  </label>
                  <input
                    type="password"
                    value={providerConfig.apiKey}
                    onChange={(e) =>
                      setProviderConfig({ ...providerConfig, apiKey: e.target.value })
                    }
                    className={cn(
                      'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                      'text-foreground text-sm font-mono',
                      'focus:outline-none focus:ring-1 focus:ring-primary'
                    )}
                    placeholder={`sk-... or your ${PROVIDER_NAMES[providerConfig.type]} API key`}
                  />
                  <p className="text-xs text-muted-foreground mt-1">
                    Optional - you can add this later in Settings
                  </p>
                </div>

                <div>
                  <label className="block text-sm font-medium text-foreground mb-2">
                    Default Model
                  </label>
                  <select
                    value={providerConfig.model}
                    onChange={(e) =>
                      setProviderConfig({ ...providerConfig, model: e.target.value })
                    }
                    className={cn(
                      'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                      'text-foreground text-sm',
                      'focus:outline-none focus:ring-1 focus:ring-primary'
                    )}
                  >
                    {DEFAULT_MODELS[providerConfig.type].map((model) => (
                      <option key={model} value={model}>
                        {model}
                      </option>
                    ))}
                  </select>
                </div>
              </div>
            </div>
          )}

          {/* Demo Step */}
          {currentStep === 'demo' && (
            <div>
              <h2 className="text-xl font-bold text-foreground mb-2">
                Create Demo Workflows
              </h2>
              <p className="text-muted-foreground mb-6">
                Start with example workflows to learn how PetalFlow works.
              </p>

              <div className="space-y-3">
                <DemoOption
                  title="Hello World (Graph)"
                  description="A simple graph workflow that demonstrates basic LLM interaction with input/output nodes"
                  checked={createDemos.simple}
                  onChange={(v) => setCreateDemos({ ...createDemos, simple: v })}
                />
                <DemoOption
                  title="Research Assistant (Agent)"
                  description="An agent workflow with tool-use capabilities for researching topics"
                  checked={createDemos.agent}
                  onChange={(v) => setCreateDemos({ ...createDemos, agent: v })}
                />
              </div>
            </div>
          )}

          {/* Complete Step */}
          {currentStep === 'complete' && (
            <div className="text-center">
              <div className="w-16 h-16 mx-auto mb-6 rounded-full bg-green-500/20 flex items-center justify-center">
                <Icon name="check" size={32} className="text-green-500" />
              </div>
              <h2 className="text-xl font-bold text-foreground mb-2">
                You're All Set!
              </h2>
              <p className="text-muted-foreground mb-6 max-w-md mx-auto">
                Your workspace is ready. Start building AI workflows with the visual designer or explore the demo workflows.
              </p>
              <div className="flex flex-col gap-2 max-w-xs mx-auto">
                <a
                  href="/workflows"
                  className={cn(
                    'px-4 py-3 rounded-lg font-medium text-sm',
                    'bg-primary text-primary-foreground hover:bg-primary/90 transition-colors'
                  )}
                >
                  Go to Workflows
                </a>
                <a
                  href="/designer"
                  className={cn(
                    'px-4 py-3 rounded-lg font-medium text-sm',
                    'bg-surface-1 border border-border text-foreground hover:bg-surface-2 transition-colors'
                  )}
                >
                  Open Designer
                </a>
              </div>
            </div>
          )}

          {/* Navigation */}
          {currentStep !== 'complete' && (
            <div className="flex items-center justify-between mt-8 pt-6 border-t border-border">
              <button
                onClick={handleSkip}
                className="text-sm text-muted-foreground hover:text-foreground transition-colors"
              >
                Skip setup
              </button>
              <div className="flex items-center gap-3">
                {currentStep !== 'welcome' && (
                  <button
                    onClick={handleBack}
                    className={cn(
                      'px-4 py-2 rounded-lg text-sm font-medium',
                      'border border-border text-foreground hover:bg-surface-1 transition-colors'
                    )}
                  >
                    Back
                  </button>
                )}
                <button
                  onClick={handleNext}
                  disabled={loading}
                  className={cn(
                    'px-4 py-2 rounded-lg text-sm font-medium',
                    'bg-primary text-primary-foreground hover:bg-primary/90 transition-colors',
                    'disabled:opacity-50 disabled:cursor-not-allowed'
                  )}
                >
                  {loading ? 'Setting up...' : currentStep === 'demo' ? 'Finish' : 'Continue'}
                </button>
              </div>
            </div>
          )}

          {currentStep === 'complete' && (
            <div className="mt-8 pt-6 border-t border-border text-center">
              <button
                onClick={handleNext}
                className="text-sm text-muted-foreground hover:text-foreground transition-colors"
              >
                Close wizard
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function FeatureCard({
  icon,
  title,
  description,
}: {
  icon: IconName
  title: string
  description: string
}) {
  return (
    <div className="p-4 rounded-xl bg-surface-1 border border-border">
      <Icon
        name={icon}
        size={20}
        className="text-primary mb-2"
      />
      <div className="text-sm font-medium text-foreground">{title}</div>
      <div className="text-xs text-muted-foreground">{description}</div>
    </div>
  )
}

function DemoOption({
  title,
  description,
  checked,
  onChange,
}: {
  title: string
  description: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label
      className={cn(
        'flex items-start gap-3 p-4 rounded-xl border cursor-pointer transition-all',
        checked
          ? 'border-primary bg-accent-soft'
          : 'border-border bg-surface-1 hover:border-primary/50'
      )}
    >
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="sr-only"
      />
      <div
        className={cn(
          'w-5 h-5 rounded border-2 flex items-center justify-center flex-shrink-0 mt-0.5',
          checked ? 'border-primary bg-primary' : 'border-muted'
        )}
      >
        {checked && <Icon name="check" size={12} className="text-white" />}
      </div>
      <div>
        <div className="text-sm font-medium text-foreground">{title}</div>
        <div className="text-xs text-muted-foreground">{description}</div>
      </div>
    </label>
  )
}
