/** Standard daemon error envelope. */
export interface ApiErrorBody {
  error: {
    code: string
    message: string
    details?: string[]
  }
}

/** Auth responses. */
export interface AuthStatus {
  setup_complete: boolean
}

export interface AuthTokens {
  access_token: string
  refresh_token: string
  expires_in: number
}

export interface LoginRequest {
  username: string
  password: string
}

export interface SetupRequest {
  username: string
  password: string
}

export interface ChangePasswordRequest {
  current_password: string
  new_password: string
}

/** Health check. */
export interface HealthResponse {
  status: string
}
