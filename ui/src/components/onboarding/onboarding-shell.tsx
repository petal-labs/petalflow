import type { ReactNode } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"

interface Step {
  path: string
  label: string
}

interface OnboardingShellProps {
  steps: Step[]
  children: ReactNode
}

export function OnboardingShell({ steps, children }: OnboardingShellProps) {
  const location = useLocation()
  const navigate = useNavigate()

  const currentPath = location.pathname.split("/").pop() ?? ""
  const currentIndex = steps.findIndex((s) => s.path === currentPath)

  const canGoBack = currentIndex > 0
  const canGoNext = currentIndex < steps.length - 1
  const isLast = currentIndex === steps.length - 1

  return (
    <div className="flex min-h-screen flex-col items-center justify-center p-4">
      <div className="w-full max-w-2xl space-y-8">
        {/* Step indicator */}
        <div className="flex items-center justify-center gap-2">
          {steps.map((step, i) => (
            <div key={step.path} className="flex items-center gap-2">
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
            onClick={() => navigate(`/onboarding/${steps[currentIndex - 1].path}`)}
            disabled={!canGoBack}
          >
            Back
          </Button>
          <div className="flex gap-2">
            {canGoNext && currentIndex > 0 && (
              <Button
                variant="ghost"
                onClick={() =>
                  navigate(`/onboarding/${steps[currentIndex + 1].path}`)
                }
              >
                Skip
              </Button>
            )}
            {canGoNext ? (
              <Button
                onClick={() =>
                  navigate(`/onboarding/${steps[currentIndex + 1].path}`)
                }
              >
                Continue
              </Button>
            ) : isLast ? (
              <Button onClick={() => navigate("/workflows")}>
                Go to Workflows
              </Button>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  )
}
