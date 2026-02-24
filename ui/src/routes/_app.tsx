import { useEffect } from 'react'
import { createFileRoute, Outlet } from '@tanstack/react-router'
import { Sidebar } from '@/components/layout/sidebar'
import { TopBar } from '@/components/layout/top-bar'
import { OnboardingWizard } from '@/components/onboarding'
import { RunModal } from '@/components/run'
import { useAuthStore } from '@/stores/auth'
import { useSettingsStore } from '@/stores/settings'

export const Route = createFileRoute('/_app')({
  component: AppLayout,
})

function AppLayout() {
  const onboardingCompleted = useSettingsStore((s) => s.onboarding.completed)
  const checkAuth = useAuthStore((s) => s.checkAuth)

  useEffect(() => {
    void checkAuth()
  }, [checkAuth])

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
      <RunModal />
    </div>
  )
}
