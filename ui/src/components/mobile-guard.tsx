import type { ReactNode } from "react"

export function MobileGuard({ children }: { children: ReactNode }) {
  return (
    <>
      {/* Desktop: render normally */}
      <div className="hidden md:contents">{children}</div>

      {/* Mobile: show prompt */}
      <div className="flex min-h-screen flex-col items-center justify-center gap-4 p-6 text-center md:hidden">
        <div className="text-4xl">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="48"
            height="48"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="mx-auto text-muted-foreground"
          >
            <rect width="20" height="14" x="2" y="3" rx="2" />
            <line x1="8" x2="16" y1="21" y2="21" />
            <line x1="12" x2="12" y1="17" y2="21" />
          </svg>
        </div>
        <h1 className="text-xl font-semibold">Desktop Required</h1>
        <p className="max-w-xs text-sm text-muted-foreground">
          PetalFlow Workflow Designer is optimized for desktop browsers. Please
          use a device with a screen width of at least 768px.
        </p>
      </div>
    </>
  )
}
