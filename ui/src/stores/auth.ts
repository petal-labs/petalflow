import { create } from "zustand"
import {
  api,
  setAccessToken,
  setRefreshHandler,
  setSessionExpiredHandler,
} from "@/api/client"
import type {
  AuthStatus,
  AuthTokens,
  LoginRequest,
  SetupRequest,
} from "@/api/types"

interface AuthState {
  /** Whether the initial auth status check has completed. */
  initialized: boolean
  /** Whether the daemon has an admin account set up. */
  setupComplete: boolean | null
  /** Current username (null when logged out). */
  user: string | null
  /** True while a login/setup/refresh request is in flight. */
  loading: boolean

  /** Check daemon auth status (first load). */
  checkStatus: () => Promise<void>
  /** Log in with username/password. */
  login: (req: LoginRequest) => Promise<void>
  /** Create the initial admin account and auto-login. */
  setup: (req: SetupRequest) => Promise<void>
  /** Log out (clears token, calls daemon). */
  logout: () => Promise<void>
  /** Attempt a silent token refresh. Returns true on success. */
  refresh: () => Promise<boolean>
  /** Called when refresh fails — clear state and flag session expired. */
  handleSessionExpired: () => void
}

export const useAuthStore = create<AuthState>((set, get) => {
  // Wire refresh + session-expired handlers into the API client.
  setRefreshHandler(() => get().refresh())
  setSessionExpiredHandler(() => get().handleSessionExpired())

  return {
    initialized: false,
    setupComplete: null,
    user: null,
    loading: false,

    async checkStatus() {
      try {
        const data = await api.get<AuthStatus>("/api/auth/status", {
          noAuth: true,
          silent: true,
        })
        set({ setupComplete: data.setup_complete, initialized: true })
      } catch {
        // Daemon unreachable — leave initialized false so health poller
        // handles it. Set initialized true so the app doesn't hang.
        set({ initialized: true, setupComplete: null })
      }
    },

    async login(req) {
      set({ loading: true })
      try {
        const data = await api.post<AuthTokens>("/api/auth/login", req, {
          noAuth: true,
        })
        setAccessToken(data.access_token)
        set({ user: req.username, loading: false })
      } catch {
        set({ loading: false })
        throw undefined // re-throw so the form can catch it
      }
    },

    async setup(req) {
      set({ loading: true })
      try {
        await api.post("/api/auth/setup", req, { noAuth: true })
        // Auto-login after setup.
        const data = await api.post<AuthTokens>(
          "/api/auth/login",
          { username: req.username, password: req.password },
          { noAuth: true },
        )
        setAccessToken(data.access_token)
        set({ user: req.username, setupComplete: true, loading: false })
      } catch {
        set({ loading: false })
        throw undefined
      }
    },

    async logout() {
      try {
        await api.post("/api/auth/logout", undefined, { silent: true })
      } catch {
        // Best-effort — clear local state regardless.
      }
      setAccessToken(null)
      set({ user: null })
    },

    async refresh(): Promise<boolean> {
      try {
        const data = await api.post<AuthTokens>(
          "/api/auth/refresh",
          undefined,
          { noAuth: true, silent: true },
        )
        setAccessToken(data.access_token)
        set({ user: get().user }) // keep user, just refresh token
        return true
      } catch {
        return false
      }
    },

    handleSessionExpired() {
      setAccessToken(null)
      set({ user: null })
      // Toast is shown by the protected route redirect.
    },
  }
})
