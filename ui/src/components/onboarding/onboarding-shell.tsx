import { type ReactNode, useCallback } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { useSettingsStore } from "@/stores/settings"

interface Step {
  path: string
  label: string
  /** If provided, called before advancing. Return false to block navigation. */
  canContinue?: () => boolean
  /** Called after the Continue button is clicked and canContinue passes. */
  onContinue?: () => Promise<void> | void
  /** Hide the Skip button for this step. */
  noSkip?: boolean
}

interface OnboardingShellProps {
  steps: Step[]
  children: ReactNode
}

export type { Step as OnboardingStep }

export function OnboardingShell({ steps, children }: OnboardingShellProps) {
  const location = useLocation()
  const navigate = useNavigate()
  const saveOnboardingStep = useSettingsStore((s) => s.saveOnboardingStep)
  const completeOnboarding = useSettingsStore((s) => s.completeOnboarding)

  const currentPath = location.pathname.split("/").pop() ?? ""
  const currentIndex = steps.findIndex((s) => s.path === currentPath)
  const step = steps[currentIndex]

  const canGoBack = currentIndex > 0
  const canGoNext = currentIndex < steps.length - 1
  const isLast = currentIndex === steps.length - 1

  const goTo = useCallback(
    async (index: number) => {
      // Persist progress server-side (best-effort).
      saveOnboardingStep(index).catch(() => {})
      navigate(`/onboarding/${steps[index].path}`)
    },
    [navigate, steps, saveOnboardingStep],
  )

  const handleContinue = useCallback(async () => {
    if (step?.canContinue && !step.canContinue()) return
    if (step?.onContinue) await step.onContinue()

    if (canGoNext) {
      goTo(currentIndex + 1)
    }
  }, [step, canGoNext, goTo, currentIndex])

  const handleFinish = useCallback(async () => {
    await completeOnboarding()
    navigate("/workflows")
  }, [completeOnboarding, navigate])

  return (
    <div className="flex min-h-screen flex-col items-center justify-center p-4">
      <div className="w-full max-w-2xl space-y-8">
        {/* Step indicator */}
        <div className="flex items-center justify-center gap-2">
          {steps.map((s, i) => (
            <div key={s.path} className="flex items-center gap-2">
              <div
                className={cn(
                  "flex h-8 w-8 items-center justify-center rounded-full text-xs font-medium",
                  i < currentIndex
                    ? "bg-primary text-primary-foreground"
                    : i === currentIndex
                      ? "border-2 border-primary text-primary"
                      : "border border-muted-foreground/30 text-muted-foreground",
                )}
              >
                {i + 1}
              </div>
              {i < steps.length - 1 && (
                <div
                  className={cn(
                    "h-px w-8",
                    i < currentIndex ? "bg-primary" : "bg-border",
                  )}
                />
              )}
            </div>
          ))}
        </div>

        {/* Step content */}
        <div className="min-h-[300px]">{children}</div>

        {/* Navigation */}
        <div className="flex justify-between">
          <Button
            variant="outline"
            onClick={() => goTo(currentIndex - 1)}
            disabled={!canGoBack}
          >
            Back
          </Button>
          <div className="flex gap-2">
            {canGoNext && !step?.noSkip && currentIndex > 0 && (
              <Button variant="ghost" onClick={() => goTo(currentIndex + 1)}>
                Skip
              </Button>
            )}
            {canGoNext ? (
              <Button onClick={handleContinue}>Continue</Button>
            ) : isLast ? (
              <Button onClick={handleFinish}>Go to Workflows</Button>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  )
}
