import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type Theme = 'light' | 'dark' | 'system'

export interface EditorPreferences {
  autoLayoutOnSave: boolean
  showPortTypes: boolean
  confirmBeforeDelete: boolean
}

export interface RunPreferences {
  streamingEnabled: boolean
  tracingEnabled: boolean
  defaultConcurrency: number
}

export interface OnboardingState {
  completed: boolean
  completedAt: string | null
  skipped: boolean
}

export interface SettingsState {
  theme: Theme
  defaultProvider: string | null
  defaultModel: string | null
  editor: EditorPreferences
  run: RunPreferences
  onboarding: OnboardingState
}

export interface SettingsActions {
  setTheme: (theme: Theme) => void
  setDefaultProvider: (provider: string | null) => void
  setDefaultModel: (model: string | null) => void
  updateEditorPreference: <K extends keyof EditorPreferences>(
    key: K,
    value: EditorPreferences[K]
  ) => void
  updateRunPreference: <K extends keyof RunPreferences>(
    key: K,
    value: RunPreferences[K]
  ) => void
  completeOnboarding: () => void
  skipOnboarding: () => void
  resetOnboarding: () => void
}

const initialState: SettingsState = {
  theme: 'dark',
  defaultProvider: null,
  defaultModel: null,
  editor: {
    autoLayoutOnSave: false,
    showPortTypes: true,
    confirmBeforeDelete: true,
  },
  run: {
    streamingEnabled: true,
    tracingEnabled: true,
    defaultConcurrency: 4,
  },
  onboarding: {
    completed: false,
    completedAt: null,
    skipped: false,
  },
}

export const useSettingsStore = create<SettingsState & SettingsActions>()(
  persist(
    (set) => ({
      ...initialState,

      setTheme: (theme) => set({ theme }),

      setDefaultProvider: (provider) => set({ defaultProvider: provider }),

      setDefaultModel: (model) => set({ defaultModel: model }),

      updateEditorPreference: (key, value) =>
        set((state) => ({
          editor: { ...state.editor, [key]: value },
        })),

      updateRunPreference: (key, value) =>
        set((state) => ({
          run: { ...state.run, [key]: value },
        })),

      completeOnboarding: () =>
        set({
          onboarding: {
            completed: true,
            completedAt: new Date().toISOString(),
            skipped: false,
          },
        }),

      skipOnboarding: () =>
        set({
          onboarding: {
            completed: true,
            completedAt: new Date().toISOString(),
            skipped: true,
          },
        }),

      resetOnboarding: () =>
        set({
          onboarding: {
            completed: false,
            completedAt: null,
            skipped: false,
          },
        }),
    }),
    {
      name: 'petalflow-settings',
    }
  )
)
