import { useEffect } from "react"
import { Routes, Route, Navigate } from "react-router-dom"
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
import { useProviderStore } from "@/stores/providers"
import { useSettingsStore } from "@/stores/settings"

function FirstWorkflowStep() {
  return (
    <p className="text-muted-foreground">Build your first workflow (coming soon)</p>
  )
}

function DoneStep() {
  return (
    <div className="text-center space-y-2">
      <p className="text-lg font-medium">You're all set!</p>
      <p className="text-muted-foreground text-sm">
        Your workspace is ready. Click "Go to Workflows" to start building.
      </p>
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
