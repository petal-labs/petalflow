import { useState } from 'react'
import { useSettingsStore } from '@/stores/settings'
import { useProviderStore, PROVIDER_NAMES, DEFAULT_MODELS } from '@/stores/provider'
import { useWorkflowStore } from '@/stores/workflow'
import { Icon, type IconName } from '@/components/ui/icon'
import { cn } from '@/lib/utils'
import type { ProviderType } from '@/lib/api-types'

type Step = 'welcome' | 'account' | 'provider' | 'demo' | 'complete'

const STEPS: { key: Step; label: string }[] = [
  { key: 'welcome', label: 'Welcome' },
  { key: 'account', label: 'Account' },
  { key: 'provider', label: 'Provider' },
  { key: 'demo', label: 'Demos' },
  { key: 'complete', label: 'Complete' },
]

interface ProviderConfig {
  type: ProviderType
  name: string
  apiKey: string
  model: string
}

interface AccountConfig {
  username: string
  password: string
  confirmPassword: string
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
  const [accountConfig, setAccountConfig] = useState<AccountConfig>({
    username: '',
    password: '',
    confirmPassword: '',
  })
  const [providerConfig, setProviderConfig] = useState<ProviderConfig>({
    type: 'anthropic',
    name: 'My Anthropic',
    apiKey: '',
    model: 'claude-sonnet-4-20250514',
  })
  const [createDemos, setCreateDemos] = useState({
    helloPetalflow: true,
    researchCritique: true,
    graphPipeline: true,
  })
  const [loading, setLoading] = useState(false)
  const [accountError, setAccountError] = useState<string | null>(null)

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
      setCurrentStep('account')
    } else if (currentStep === 'account') {
      // Validate account
      if (!accountConfig.username.trim()) {
        setAccountError('Username is required')
        return
      }
      if (accountConfig.password.length < 6) {
        setAccountError('Password must be at least 6 characters')
        return
      }
      if (accountConfig.password !== accountConfig.confirmPassword) {
        setAccountError('Passwords do not match')
        return
      }
      setAccountError(null)
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

        // Create demo workflows based on selection
        if (createDemos.helloPetalflow) {
          // Hello PetalFlow - Agent/Task: 1 agent, 1 task, sequential
          await createAgentWorkflow({
            version: '1.0.0',
            kind: 'agent_workflow',
            id: crypto.randomUUID(),
            name: 'Hello PetalFlow',
            agents: {
              greeter: {
                id: 'greeter',
                role: 'Friendly Greeter',
                goal: 'Welcome users and demonstrate basic LLM interaction',
                provider: providerConfig.type,
                model: providerConfig.model,
              },
            },
            tasks: {
              greet: {
                id: 'greet',
                description: 'Say hello and provide a brief, friendly welcome message about PetalFlow',
                agent: 'greeter',
                expected_output: 'A warm welcome message',
              },
            },
            execution: {
              strategy: 'sequential',
              task_order: ['greet'],
            },
          })
        }

        if (createDemos.researchCritique) {
          // Research & Critique - Agent/Task: 3 agents, sequential
          await createAgentWorkflow({
            version: '1.0.0',
            kind: 'agent_workflow',
            id: crypto.randomUUID(),
            name: 'Research & Critique',
            agents: {
              researcher: {
                id: 'researcher',
                role: 'Research Analyst',
                goal: 'Gather comprehensive information about topics',
                provider: providerConfig.type,
                model: providerConfig.model,
              },
              critic: {
                id: 'critic',
                role: 'Critical Reviewer',
                goal: 'Identify gaps, biases, and areas for improvement in research',
                provider: providerConfig.type,
                model: providerConfig.model,
              },
              writer: {
                id: 'writer',
                role: 'Technical Writer',
                goal: 'Synthesize research and critique into polished output',
                provider: providerConfig.type,
                model: providerConfig.model,
              },
            },
            tasks: {
              research: {
                id: 'research',
                description: 'Research {{input.topic}} thoroughly. Gather key facts, statistics, and perspectives.',
                agent: 'researcher',
                expected_output: 'Structured research notes with sources',
              },
              critique: {
                id: 'critique',
                description: 'Review the research from {{tasks.research.output}}. Identify gaps, potential biases, and suggest improvements.',
                agent: 'critic',
                expected_output: 'Critical analysis with specific improvement suggestions',
                depends_on: ['research'],
              },
              write: {
                id: 'write',
                description: 'Using {{tasks.research.output}} and {{tasks.critique.output}}, write a balanced, well-structured report.',
                agent: 'writer',
                expected_output: 'Final polished report',
                depends_on: ['critique'],
              },
            },
            execution: {
              strategy: 'sequential',
              task_order: ['research', 'critique', 'write'],
            },
          })
        }

        if (createDemos.graphPipeline) {
          // Graph Pipeline - Graph IR: input → template_render → llm_prompt → output
          await createGraphWorkflow({
            id: crypto.randomUUID(),
            version: '1.0.0',
            metadata: { name: 'Graph Pipeline' },
            nodes: [
              {
                id: 'input',
                type: 'input',
                config: {
                  schema: {
                    type: 'object',
                    properties: {
                      topic: { type: 'string', description: 'The topic to explore' },
                    },
                    required: ['topic'],
                  },
                },
              },
              {
                id: 'template',
                type: 'template_render',
                config: {
                  template: 'Write a concise explanation of: {{topic}}',
                },
              },
              {
                id: 'llm',
                type: 'llm_prompt',
                config: {
                  provider: providerConfig.type,
                  model: providerConfig.model,
                  system: 'You are a helpful assistant that provides clear, educational explanations.',
                },
              },
              {
                id: 'output',
                type: 'output',
                config: {},
              },
            ],
            edges: [
              { source: 'input', source_handle: 'topic', target: 'template', target_handle: 'topic' },
              { source: 'template', source_handle: 'rendered', target: 'llm', target_handle: 'prompt' },
              { source: 'llm', source_handle: 'response', target: 'output', target_handle: 'result' },
            ],
            entry: 'input',
          })
        }
      } catch {
        // Continue even if creation fails - user can set up manually
      }
      setLoading(false)
      setCurrentStep('complete')
    }
  }

  const handleBack = () => {
    if (currentStep === 'account') {
      setCurrentStep('welcome')
    } else if (currentStep === 'provider') {
      setCurrentStep('account')
    } else if (currentStep === 'demo') {
      setCurrentStep('provider')
    }
  }

  const handleSkip = () => {
    skipOnboarding()
  }

  const handleComplete = (navigateTo: string) => {
    completeOnboarding()
    window.location.href = navigateTo
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
                    'w-8 h-0.5',
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
              <p className="text-muted-foreground mb-4 max-w-md mx-auto">
                PetalFlow runs AI workflows. Choose <strong>Agent mode</strong> for simple multi-step tasks,
                or <strong>Graph mode</strong> for full control. Same engine either way.
              </p>
              <div className="grid grid-cols-3 gap-4 text-left mb-6">
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

          {/* Account Step */}
          {currentStep === 'account' && (
            <div>
              <h2 className="text-xl font-bold text-foreground mb-2">
                Create Your Account
              </h2>
              <p className="text-muted-foreground mb-6">
                Set up a local account to secure your workspace.
              </p>

              {accountError && (
                <div className="p-3 mb-4 rounded-lg bg-red-500/10 border border-red-500/30 text-red-500 text-sm">
                  {accountError}
                </div>
              )}

              <div className="space-y-4">
                <div>
                  <label className="block text-sm font-medium text-foreground mb-2">
                    Username
                  </label>
                  <input
                    type="text"
                    value={accountConfig.username}
                    onChange={(e) =>
                      setAccountConfig({ ...accountConfig, username: e.target.value })
                    }
                    className={cn(
                      'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                      'text-foreground text-sm',
                      'focus:outline-none focus:ring-1 focus:ring-primary'
                    )}
                    placeholder="Enter a username"
                    autoFocus
                  />
                </div>

                <div>
                  <label className="block text-sm font-medium text-foreground mb-2">
                    Password
                  </label>
                  <input
                    type="password"
                    value={accountConfig.password}
                    onChange={(e) =>
                      setAccountConfig({ ...accountConfig, password: e.target.value })
                    }
                    className={cn(
                      'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                      'text-foreground text-sm',
                      'focus:outline-none focus:ring-1 focus:ring-primary'
                    )}
                    placeholder="At least 6 characters"
                  />
                </div>

                <div>
                  <label className="block text-sm font-medium text-foreground mb-2">
                    Confirm Password
                  </label>
                  <input
                    type="password"
                    value={accountConfig.confirmPassword}
                    onChange={(e) =>
                      setAccountConfig({ ...accountConfig, confirmPassword: e.target.value })
                    }
                    className={cn(
                      'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                      'text-foreground text-sm',
                      'focus:outline-none focus:ring-1 focus:ring-primary'
                    )}
                    placeholder="Re-enter your password"
                  />
                </div>
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
                    placeholder={`Enter your ${PROVIDER_NAMES[providerConfig.type]} API key`}
                  />
                  <p className="text-xs text-muted-foreground mt-1">
                    Optional for now - you can add this later in Providers settings
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
                Install Demo Workflows
              </h2>
              <p className="text-muted-foreground mb-6">
                These examples help you learn PetalFlow quickly. Each includes sample inputs and can be run immediately.
              </p>

              <div className="space-y-3">
                <DemoOption
                  title="Hello PetalFlow"
                  badge="Agent/Task"
                  description="Minimal hello world - 1 agent, 1 task. Proves your provider works."
                  checked={createDemos.helloPetalflow}
                  onChange={(v) => setCreateDemos({ ...createDemos, helloPetalflow: v })}
                />
                <DemoOption
                  title="Research & Critique"
                  badge="Agent/Task"
                  description="Multi-agent workflow: researcher → critic → writer. Shows visible handoffs between agents."
                  checked={createDemos.researchCritique}
                  onChange={(v) => setCreateDemos({ ...createDemos, researchCritique: v })}
                />
                <DemoOption
                  title="Graph Pipeline"
                  badge="Graph IR"
                  description="Raw graph mode: input → template → LLM → output. Shows the low-level execution model."
                  checked={createDemos.graphPipeline}
                  onChange={(v) => setCreateDemos({ ...createDemos, graphPipeline: v })}
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
                Your workspace is ready. Explore your demo workflows and start building.
              </p>
              <div className="flex flex-col gap-2 max-w-xs mx-auto">
                <button
                  onClick={() => handleComplete('/workflows')}
                  className={cn(
                    'px-4 py-3 rounded-lg font-medium text-sm',
                    'bg-primary text-primary-foreground hover:bg-primary/90 transition-colors'
                  )}
                >
                  Go to Workflows
                </button>
                <button
                  onClick={() => handleComplete('/designer')}
                  className={cn(
                    'px-4 py-3 rounded-lg font-medium text-sm',
                    'bg-surface-1 border border-border text-foreground hover:bg-surface-2 transition-colors'
                  )}
                >
                  Open Designer
                </button>
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
                  {loading ? 'Setting up...' : currentStep === 'demo' ? 'Finish Setup' : 'Continue'}
                </button>
              </div>
            </div>
          )}

          {currentStep === 'complete' && (
            <div className="mt-8 pt-6 border-t border-border text-center">
              <button
                onClick={() => handleComplete('/workflows')}
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
  badge,
  description,
  checked,
  onChange,
}: {
  title: string
  badge: string
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
      <div className="flex-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium text-foreground">{title}</span>
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface-2 text-muted-foreground font-medium">
            {badge}
          </span>
        </div>
        <div className="text-xs text-muted-foreground mt-0.5">{description}</div>
      </div>
    </label>
  )
}
