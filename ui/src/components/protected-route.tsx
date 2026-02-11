import { Navigate, useLocation } from "react-router-dom"
import { useAuthStore } from "@/stores/auth"

/**
 * Wraps routes that require authentication.
 * Redirects to /login (or /setup if not yet set up) when the user
 * is not authenticated.
 */
export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { initialized, setupComplete, user } = useAuthStore()
  const location = useLocation()

  // Still loading auth status — show nothing (or a spinner).
  if (!initialized) {
    return null
  }

  // First-run: redirect to account creation.
  if (setupComplete === false) {
    return <Navigate to="/setup" replace />
  }

  // Not logged in.
  if (!user) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  return <>{children}</>
}
