import { Routes, Route, Navigate } from "react-router-dom"
import { OnboardingShell } from "@/components/onboarding/onboarding-shell"

// Step components — stubs for now, fleshed out in P1-25..P1-27
function WelcomeStep() {
  return <p className="text-muted-foreground">Welcome to PetalFlow</p>
}

function ProvidersStep() {
  return <p className="text-muted-foreground">Configure LLM providers</p>
}

function ToolsStep() {
  return <p className="text-muted-foreground">Register tools</p>
}

function FirstWorkflowStep() {
  return <p className="text-muted-foreground">Build your first workflow</p>
}

function DoneStep() {
  return <p className="text-muted-foreground">Setup complete!</p>
}

const steps = [
  { path: "welcome", label: "Welcome", element: <WelcomeStep /> },
  { path: "providers", label: "Providers", element: <ProvidersStep /> },
  { path: "tools", label: "Tools", element: <ToolsStep /> },
  { path: "first-workflow", label: "First Workflow", element: <FirstWorkflowStep /> },
  { path: "done", label: "Done", element: <DoneStep /> },
]

export { steps as onboardingSteps }

export default function OnboardingPage() {
  return (
    <OnboardingShell steps={steps}>
      <Routes>
        <Route index element={<Navigate to="welcome" replace />} />
        {steps.map((step) => (
          <Route key={step.path} path={step.path} element={step.element} />
        ))}
      </Routes>
    </OnboardingShell>
  )
}
