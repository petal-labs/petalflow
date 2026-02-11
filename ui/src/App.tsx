import { lazy, Suspense, useEffect } from "react"
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom"
import { Toaster } from "@/components/ui/sonner"
import { MobileGuard } from "@/components/mobile-guard"
import { ProtectedRoute } from "@/components/protected-route"
import { DaemonStatus } from "@/components/daemon-status"
import { AppShell } from "@/components/app-shell"
import { useAuthStore } from "@/stores/auth"

// Route-level code splitting — each page loads its own chunk
const WorkflowsPage = lazy(() => import("@/pages/workflows"))
const WorkflowEditorPage = lazy(() => import("@/pages/workflow-editor"))
const RunsPage = lazy(() => import("@/pages/runs"))
const RunDetailPage = lazy(() => import("@/pages/run-detail"))
const SettingsPage = lazy(() => import("@/pages/settings"))
const OnboardingPage = lazy(() => import("@/pages/onboarding"))
const LoginPage = lazy(() => import("@/pages/login"))
const SetupPage = lazy(() => import("@/pages/setup"))
const NotFoundPage = lazy(() => import("@/pages/not-found"))

// Settings sub-pages
const AccountSettings = lazy(() =>
  import("@/components/settings/account-settings").then((m) => ({
    default: m.AccountSettings,
  })),
)
const ProvidersSettings = lazy(() =>
  import("@/components/settings/providers-settings").then((m) => ({
    default: m.ProvidersSettings,
  })),
)
const ToolsSettings = lazy(() =>
  import("@/components/settings/tools-settings").then((m) => ({
    default: m.ToolsSettings,
  })),
)
const PreferencesSettings = lazy(() =>
  import("@/components/settings/preferences-settings").then((m) => ({
    default: m.PreferencesSettings,
  })),
)
const AboutSettings = lazy(() =>
  import("@/components/settings/about-settings").then((m) => ({
    default: m.AboutSettings,
  })),
)

function PageFallback() {
  return (
    <div className="flex h-[calc(100vh-3.5rem)] items-center justify-center text-sm text-muted-foreground">
      Loading...
    </div>
  )
}

export default function App() {
  const checkStatus = useAuthStore((s) => s.checkStatus)

  useEffect(() => {
    checkStatus()
  }, [checkStatus])

  return (
    <MobileGuard>
      <BrowserRouter>
        <Suspense fallback={<PageFallback />}>
          <Routes>
            {/* Public routes */}
            <Route path="/login" element={<LoginPage />} />
            <Route path="/setup" element={<SetupPage />} />

            {/* Onboarding wizard (authenticated, no app shell nav) */}
            <Route
              path="/onboarding/*"
              element={
                <ProtectedRoute>
                  <OnboardingPage />
                </ProtectedRoute>
              }
            />

            {/* App shell with nav (authenticated) */}
            <Route
              element={
                <ProtectedRoute>
                  <AppShell />
                </ProtectedRoute>
              }
            >
              <Route index element={<Navigate to="/workflows" replace />} />
              <Route path="/workflows" element={<WorkflowsPage />} />
              <Route
                path="/workflows/:id/edit"
                element={<WorkflowEditorPage />}
              />
              <Route path="/runs" element={<RunsPage />} />
              <Route path="/runs/:runId" element={<RunDetailPage />} />
              <Route path="/settings" element={<SettingsPage />}>
                <Route
                  index
                  element={<Navigate to="/settings/account" replace />}
                />
                <Route path="account" element={<AccountSettings />} />
                <Route
                  path="providers"
                  element={<ProvidersSettings />}
                />
                <Route path="tools" element={<ToolsSettings />} />
                <Route
                  path="preferences"
                  element={<PreferencesSettings />}
                />
                <Route
                  path="about"
                  element={<AboutSettings />}
                />
              </Route>
            </Route>

            <Route path="*" element={<NotFoundPage />} />
          </Routes>
        </Suspense>
        <Toaster />
        <DaemonStatus />
      </BrowserRouter>
    </MobileGuard>
  )
}
