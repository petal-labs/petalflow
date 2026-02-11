import { useEffect } from "react"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useSettingsStore } from "@/stores/settings"
import type { UserPreferences } from "@/api/types"

export function PreferencesSettings() {
  const settings = useSettingsStore((s) => s.settings)
  const loading = useSettingsStore((s) => s.loading)
  const fetchSettings = useSettingsStore((s) => s.fetchSettings)
  const updatePreferences = useSettingsStore((s) => s.updatePreferences)

  useEffect(() => {
    if (!settings) fetchSettings()
  }, [settings, fetchSettings])

  if (loading || !settings) {
    return <p className="text-sm text-muted-foreground">Loading preferences...</p>
  }

  const prefs = settings.preferences

  const update = (patch: Partial<UserPreferences>) => {
    updatePreferences(patch)
  }

  return (
    <div className="space-y-6 max-w-lg">
      <div>
        <h2 className="text-lg font-medium">Preferences</h2>
        <p className="text-sm text-muted-foreground">
          Customize your PetalFlow workspace.
        </p>
      </div>

      {/* Theme */}
      <div className="space-y-1.5">
        <Label className="text-xs">Theme</Label>
        <Select
          value={prefs.theme ?? "system"}
          onValueChange={(v) => update({ theme: v as UserPreferences["theme"] })}
        >
          <SelectTrigger className="w-40">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="system">System</SelectItem>
            <SelectItem value="light">Light</SelectItem>
            <SelectItem value="dark">Dark</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Default new workflow mode */}
      <div className="space-y-1.5">
        <Label className="text-xs">Default new workflow mode</Label>
        <Select
          value={prefs.default_workflow_mode ?? "agent_task"}
          onValueChange={(v) =>
            update({ default_workflow_mode: v as "agent_task" | "graph" })
          }
        >
          <SelectTrigger className="w-40">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="agent_task">Agent / Task</SelectItem>
            <SelectItem value="graph">Graph</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Auto-save interval */}
      <div className="space-y-1.5">
        <Label className="text-xs">Auto-save interval (ms)</Label>
        <Input
          type="number"
          min={500}
          max={30000}
          step={500}
          value={prefs.auto_save_interval_ms ?? 2000}
          onChange={(e) =>
            update({ auto_save_interval_ms: Number(e.target.value) || 2000 })
          }
          className="w-32"
        />
      </div>

      {/* Output format */}
      <div className="space-y-1.5">
        <Label className="text-xs">Output format</Label>
        <Select
          value={prefs.output_format ?? "markdown"}
          onValueChange={(v) =>
            update({ output_format: v as "markdown" | "plain" | "json" })
          }
        >
          <SelectTrigger className="w-40">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="markdown">Markdown</SelectItem>
            <SelectItem value="plain">Plain text</SelectItem>
            <SelectItem value="json">JSON</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Toggle preferences */}
      <div className="space-y-3 pt-2 border-t">
        <label className="flex items-center gap-2 cursor-pointer">
          <Checkbox
            checked={prefs.tracing_default ?? true}
            onCheckedChange={(c) => update({ tracing_default: c === true })}
          />
          <div>
            <div className="text-xs">Enable tracing by default</div>
            <div className="text-[10px] text-muted-foreground">
              New runs will have tracing enabled
            </div>
          </div>
        </label>

        <label className="flex items-center gap-2 cursor-pointer">
          <Checkbox
            checked={prefs.snap_to_grid ?? true}
            onCheckedChange={(c) => update({ snap_to_grid: c === true })}
          />
          <div>
            <div className="text-xs">Canvas snap-to-grid</div>
            <div className="text-[10px] text-muted-foreground">
              Align nodes to a grid in graph mode
            </div>
          </div>
        </label>

        <label className="flex items-center gap-2 cursor-pointer">
          <Checkbox
            checked={prefs.show_port_types ?? true}
            onCheckedChange={(c) => update({ show_port_types: c === true })}
          />
          <div>
            <div className="text-xs">Show node port types</div>
            <div className="text-[10px] text-muted-foreground">
              Display type annotations on port handles
            </div>
          </div>
        </label>
      </div>
    </div>
  )
}
