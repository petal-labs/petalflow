import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"

export function WelcomeStep() {
  return (
    <Card>
      <CardHeader className="text-center">
        <CardTitle className="text-2xl">Welcome to PetalFlow</CardTitle>
        <CardDescription>
          Let's get your workspace set up in about 5 minutes
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-6 text-sm text-muted-foreground">
        <p>
          PetalFlow lets you build AI workflows in two modes:
        </p>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="rounded-lg border p-4 space-y-1">
            <p className="font-medium text-foreground">Agent / Task Mode</p>
            <p>
              Define agents with roles and tools, then compose tasks that
              describe what to accomplish. PetalFlow compiles it into an
              execution graph automatically.
            </p>
          </div>
          <div className="rounded-lg border p-4 space-y-1">
            <p className="font-medium text-foreground">Graph Mode</p>
            <p>
              Build node-and-edge graphs directly for full control over
              branching, loops, gates, and parallel execution paths.
            </p>
          </div>
        </div>
        <div className="rounded-lg bg-muted/50 p-4 space-y-2">
          <p className="font-medium text-foreground">This wizard will help you:</p>
          <ol className="list-inside list-decimal space-y-1">
            <li>Connect at least one LLM provider (API key required)</li>
            <li>Register external tools your agents can use</li>
            <li>Build and run your first workflow</li>
          </ol>
        </div>
        <p className="text-center text-xs text-muted-foreground/70">
          Each step can be skipped and revisited later from Settings.
        </p>
      </CardContent>
    </Card>
  )
}
