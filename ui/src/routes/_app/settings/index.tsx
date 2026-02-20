import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/_app/settings/')({
  component: SettingsPage,
})

function SettingsPage() {
  return (
    <div className="p-7">
      <h1 className="text-xl font-bold">Settings</h1>
      <p className="text-muted-foreground mt-1">Settings coming in Phase 9</p>
    </div>
  )
}
