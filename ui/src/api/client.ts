import { toast } from "sonner"
import type { ApiErrorBody } from "./types"

// ---------------------------------------------------------------------------
// Daemon URL resolution (Spec §1.6)
// ---------------------------------------------------------------------------

function resolveDaemonUrl(): string {
  // 1. URL parameter: ?daemon=http://host:port
  const params = new URLSearchParams(window.location.search)
  const fromParam = params.get("daemon")
  if (fromParam) return fromParam.replace(/\/+$/, "")

  // 2. In production (embedded SPA), same origin — no config needed.
  //    In dev, the Vite proxy handles /api/* → localhost:8080.
  return ""
}

const BASE_URL = resolveDaemonUrl()

// ---------------------------------------------------------------------------
// Token management (set by auth store)
// ---------------------------------------------------------------------------

let accessToken: string | null = null
let refreshPromise: Promise<boolean> | null = null

/** Called by auth store to set/clear the in-memory access token. */
export function setAccessToken(token: string | null) {
  accessToken = token
}

export function getAccessToken(): string | null {
  return accessToken
}

// ---------------------------------------------------------------------------
// Refresh callback (injected by auth store to avoid circular imports)
// ---------------------------------------------------------------------------

type RefreshFn = () => Promise<boolean>
let refreshFn: RefreshFn | null = null

export function setRefreshHandler(fn: RefreshFn | null) {
  refreshFn = fn
}

// Session expiry callback
type SessionExpiredFn = () => void
let sessionExpiredFn: SessionExpiredFn | null = null

export function setSessionExpiredHandler(fn: SessionExpiredFn | null) {
  sessionExpiredFn = fn
}

// ---------------------------------------------------------------------------
// Error class
// ---------------------------------------------------------------------------

export class ApiError extends Error {
  status: number
  code: string
  details?: string[]

  constructor(status: number, code: string, message: string, details?: string[]) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
    this.details = details
  }
}

// ---------------------------------------------------------------------------
// Core fetch wrapper
// ---------------------------------------------------------------------------

interface RequestOptions extends Omit<RequestInit, "body"> {
  body?: unknown
  /** Skip auth header (for login/setup/health endpoints). */
  noAuth?: boolean
  /** Skip toast on error. */
  silent?: boolean
}

async function request<T>(
  path: string,
  opts: RequestOptions = {},
): Promise<T> {
  const { body, noAuth, silent, ...init } = opts

  const headers = new Headers(init.headers)
  if (body !== undefined) {
    headers.set("Content-Type", "application/json")
  }
  if (!noAuth && accessToken) {
    headers.set("Authorization", `Bearer ${accessToken}`)
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    ...init,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  // --- Handle 401 with token refresh ---
  if (res.status === 401 && !noAuth && refreshFn) {
    // Deduplicate concurrent refreshes.
    if (!refreshPromise) {
      refreshPromise = refreshFn().finally(() => {
        refreshPromise = null
      })
    }
    const refreshed = await refreshPromise
    if (refreshed) {
      // Retry the original request with the new token.
      return request<T>(path, opts)
    }
    // Refresh failed — session expired.
    if (sessionExpiredFn) sessionExpiredFn()
    throw new ApiError(401, "session_expired", "Session expired")
  }

  // --- Parse response ---
  if (res.status === 204) {
    return undefined as T
  }

  const text = await res.text()
  let data: T | ApiErrorBody

  try {
    data = JSON.parse(text)
  } catch {
    if (!res.ok) {
      const err = new ApiError(res.status, "unknown", text || res.statusText)
      if (!silent) showErrorToast(err)
      throw err
    }
    return text as T
  }

  if (!res.ok) {
    const errBody = data as ApiErrorBody
    const err = new ApiError(
      res.status,
      errBody.error?.code ?? "unknown",
      errBody.error?.message ?? res.statusText,
      errBody.error?.details,
    )
    if (!silent) showErrorToast(err)
    throw err
  }

  return data as T
}

function showErrorToast(err: ApiError) {
  // Don't toast 401s — handled by session expiry flow.
  if (err.status === 401) return

  toast.error(err.message, {
    description: err.details?.join(", "),
  })
}

// ---------------------------------------------------------------------------
// Public API methods
// ---------------------------------------------------------------------------

export const api = {
  get<T>(path: string, opts?: RequestOptions) {
    return request<T>(path, { ...opts, method: "GET" })
  },

  post<T>(path: string, body?: unknown, opts?: RequestOptions) {
    return request<T>(path, { ...opts, method: "POST", body })
  },

  put<T>(path: string, body?: unknown, opts?: RequestOptions) {
    return request<T>(path, { ...opts, method: "PUT", body })
  },

  delete<T>(path: string, opts?: RequestOptions) {
    return request<T>(path, { ...opts, method: "DELETE" })
  },
}
