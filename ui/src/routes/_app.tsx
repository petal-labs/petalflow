import { createFileRoute, Outlet } from '@tanstack/react-router'
import { Sidebar } from '@/components/layout/sidebar'
import { TopBar } from '@/components/layout/top-bar'
import { OnboardingWizard } from '@/components/onboarding'
import { useSettingsStore } from '@/stores/settings'

export const Route = createFileRoute('/_app')({
  component: AppLayout,
})

function AppLayout() {
  const onboardingCompleted = useSettingsStore((s) => s.onboarding.completed)

  if (!onboardingCompleted) {
    return <OnboardingWizard />
  }

  return (
    <div className="h-screen flex flex-col overflow-hidden bg-surface-1">
      <TopBar />
      <div className="flex flex-1 overflow-hidden">
        <Sidebar />
        <main className="flex-1 overflow-auto flex flex-col">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
