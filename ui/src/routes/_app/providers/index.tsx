import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/_app/providers/')({
  component: ProvidersPage,
})

function ProvidersPage() {
  return (
    <div className="p-7">
      <h1 className="text-xl font-bold">Providers</h1>
      <p className="text-muted-foreground mt-1">Provider management coming in Phase 8</p>
    </div>
  )
}
