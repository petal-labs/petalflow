import { useEffect } from "react"
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom"
import { Toaster } from "@/components/ui/sonner"
import { MobileGuard } from "@/components/mobile-guard"
import { ProtectedRoute } from "@/components/protected-route"
import { DaemonStatus } from "@/components/daemon-status"
import { AppShell } from "@/components/app-shell"
import { useAuthStore } from "@/stores/auth"
import WorkflowsPage from "@/pages/workflows"
import WorkflowEditorPage from "@/pages/workflow-editor"
import RunsPage from "@/pages/runs"
import SettingsPage from "@/pages/settings"
import OnboardingPage from "@/pages/onboarding"
import LoginPage from "@/pages/login"
import SetupPage from "@/pages/setup"
import NotFoundPage from "@/pages/not-found"
import { AccountSettings } from "@/components/settings/account-settings"
import { ProvidersSettings } from "@/components/settings/providers-settings"

export default function App() {
  const checkStatus = useAuthStore((s) => s.checkStatus)

  useEffect(() => {
    checkStatus()
  }, [checkStatus])

  return (
    <MobileGuard>
      <BrowserRouter>
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
              <Route
                path="tools"
                element={
                  <p className="text-muted-foreground">Tool registry</p>
                }
              />
              <Route
                path="preferences"
                element={<p className="text-muted-foreground">Preferences</p>}
              />
              <Route
                path="about"
                element={<p className="text-muted-foreground">About</p>}
              />
            </Route>
          </Route>

          <Route path="*" element={<NotFoundPage />} />
        </Routes>
        <Toaster />
        <DaemonStatus />
      </BrowserRouter>
    </MobileGuard>
  )
}
