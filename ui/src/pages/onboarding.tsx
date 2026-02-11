import { useEffect, useState } from "react"
import { Routes, Route, Navigate, useNavigate } from "react-router-dom"
import {
  OnboardingShell,
  type OnboardingStep,
} from "@/components/onboarding/onboarding-shell"
import { WelcomeStep } from "@/components/onboarding/welcome-step"
import {
  ProvidersStep,
  useHasProvider,
} from "@/components/onboarding/providers-step"
import { ToolsStep } from "@/components/onboarding/tools-step"
import { Checkbox } from "@/components/ui/checkbox"
import { useProviderStore } from "@/stores/providers"
import { useToolStore } from "@/stores/tools"
import { useSettingsStore } from "@/stores/settings"

function FirstWorkflowStep() {
  return (
    <p className="text-muted-foreground">Build your first workflow (coming soon)</p>
  )
}

function DoneStep() {
  const providers = useProviderStore((s) => s.providers)
  const tools = useToolStore((s) => s.tools)
  const updatePreferences = useSettingsStore((s) => s.updatePreferences)
  const navigate = useNavigate()
  const [showTips, setShowTips] = useState(true)

  const readyTools = tools.filter((t) => t.status === "ready")

  return (
    <div className="space-y-6 max-w-md mx-auto">
      <div className="text-center space-y-2">
        <p className="text-lg font-medium">You're all set!</p>
        <p className="text-muted-foreground text-sm">
          Your workspace is configured and ready.
        </p>
      </div>

      {/* Config summary */}
      <div className="rounded border divide-y text-sm">
        <div className="flex items-center justify-between px-4 py-2.5">
          <span className="text-muted-foreground">LLM Providers</span>
          <span className="font-medium">
            {providers.length} configured
          </span>
        </div>
        <div className="flex items-center justify-between px-4 py-2.5">
          <span className="text-muted-foreground">Tools</span>
          <span className="font-medium">
            {readyTools.length} ready
          </span>
        </div>
      </div>

      {/* Quick-action cards */}
      <div className="grid gap-2">
        <button
          type="button"
          className="flex items-center gap-3 rounded border p-3 text-left text-sm hover:bg-muted/50 transition-colors"
          onClick={() => navigate("/workflows")}
        >
          <span className="text-xl">+</span>
          <div>
            <div className="font-medium">Build a new workflow</div>
            <div className="text-xs text-muted-foreground">
              Create an Agent/Task or Graph workflow
            </div>
          </div>
        </button>
        <button
          type="button"
          className="flex items-center gap-3 rounded border p-3 text-left text-sm hover:bg-muted/50 transition-colors"
          onClick={() => navigate("/settings/tools")}
        >
          <span className="text-xl">*</span>
          <div>
            <div className="font-medium">Explore tool registry</div>
            <div className="text-xs text-muted-foreground">
              Register MCP servers and HTTP tools
            </div>
          </div>
        </button>
      </div>

      {/* Show tips checkbox */}
      <label className="flex items-center gap-2 cursor-pointer text-sm">
        <Checkbox
          checked={showTips}
          onCheckedChange={(checked) => {
            const val = checked === true
            setShowTips(val)
            updatePreferences({ show_tips: val } as Record<string, unknown>)
          }}
        />
        <span>Show tips in the designer</span>
      </label>
    </div>
  )
}

function OnboardingContent() {
  const hasProvider = useHasProvider()
  const fetchProviders = useProviderStore((s) => s.fetchProviders)
  const fetchSettings = useSettingsStore((s) => s.fetchSettings)

  useEffect(() => {
    fetchProviders()
    fetchSettings()
  }, [fetchProviders, fetchSettings])

  const steps: OnboardingStep[] = [
    {
      path: "welcome",
      label: "Welcome",
      noSkip: true,
    },
    {
      path: "providers",
      label: "Providers",
      noSkip: true,
      canContinue: () => hasProvider,
    },
    { path: "tools", label: "Tools" },
    { path: "first-workflow", label: "First Workflow" },
    { path: "done", label: "Done" },
  ]

  return (
    <OnboardingShell steps={steps}>
      <Routes>
        <Route index element={<Navigate to="welcome" replace />} />
        <Route path="welcome" element={<WelcomeStep />} />
        <Route path="providers" element={<ProvidersStep />} />
        <Route path="tools" element={<ToolsStep />} />
        <Route path="first-workflow" element={<FirstWorkflowStep />} />
        <Route path="done" element={<DoneStep />} />
      </Routes>
    </OnboardingShell>
  )
}

export default function OnboardingPage() {
  return <OnboardingContent />
}
