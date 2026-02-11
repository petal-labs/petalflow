import { create } from "zustand"
import { api } from "@/api/client"
import type { AppSettings, UserPreferences } from "@/api/types"

interface SettingsState {
  settings: AppSettings | null
  loading: boolean

  fetchSettings: () => Promise<void>
  updateSettings: (patch: Partial<AppSettings>) => Promise<void>
  updatePreferences: (prefs: Partial<UserPreferences>) => Promise<void>
  /** Mark onboarding as complete. */
  completeOnboarding: () => Promise<void>
  /** Save the current onboarding step for resume. */
  saveOnboardingStep: (step: number) => Promise<void>
}

export const useSettingsStore = create<SettingsState>((set, get) => ({
  settings: null,
  loading: false,

  async fetchSettings() {
    set({ loading: true })
    try {
      const data = await api.get<AppSettings>("/api/settings")
      set({ settings: data, loading: false })
    } catch {
      set({ loading: false })
    }
  },

  async updateSettings(patch) {
    const current = get().settings
    const merged = { ...current, ...patch }
    await api.put("/api/settings", merged)
    set({ settings: merged as AppSettings })
  },

  async updatePreferences(prefs) {
    const current = get().settings
    if (!current) return
    const merged: AppSettings = {
      ...current,
      preferences: { ...current.preferences, ...prefs },
    }
    await api.put("/api/settings", merged)
    set({ settings: merged })
  },

  async completeOnboarding() {
    await get().updateSettings({ onboarding_complete: true })
  },

  async saveOnboardingStep(step) {
    await get().updateSettings({ onboarding_step: step })
  },
}))
