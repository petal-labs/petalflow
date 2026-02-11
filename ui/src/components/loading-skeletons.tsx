import { Skeleton } from "@/components/ui/skeleton"

/** Skeleton for the workflow card grid (WorkflowsPage) */
export function WorkflowCardsSkeleton({ count = 8 }: { count?: number }) {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className="rounded-lg border p-4 space-y-3">
          <div className="flex items-center justify-between">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-5 w-16 rounded-full" />
          </div>
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-2/3" />
          <div className="flex gap-1 pt-1">
            <Skeleton className="h-4 w-12 rounded-full" />
            <Skeleton className="h-4 w-10 rounded-full" />
          </div>
        </div>
      ))}
    </div>
  )
}

/** Skeleton for the runs table (RunsPage) */
export function RunsTableSkeleton({ rows = 6 }: { rows?: number }) {
  return (
    <div className="rounded border">
      {/* Header row */}
      <div className="flex items-center gap-4 border-b px-4 py-2.5">
        <Skeleton className="h-3 w-36" />
        <Skeleton className="h-3 w-28" />
        <Skeleton className="h-3 w-16" />
        <Skeleton className="h-3 w-32" />
        <Skeleton className="h-3 w-16" />
      </div>
      {/* Data rows */}
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-4 border-b last:border-b-0 px-4 py-3">
          <Skeleton className="h-3 w-36" />
          <Skeleton className="h-3 w-28" />
          <Skeleton className="h-5 w-16 rounded-full" />
          <Skeleton className="h-3 w-32" />
          <Skeleton className="h-3 w-14" />
        </div>
      ))}
    </div>
  )
}

/** Skeleton for the tools table (ToolsSettings) */
export function ToolsTableSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="rounded border">
      <div className="flex items-center gap-4 border-b px-4 py-2.5">
        <Skeleton className="h-3 w-6" />
        <Skeleton className="h-3 w-32" />
        <Skeleton className="h-3 w-20" />
        <Skeleton className="h-3 w-20" />
        <Skeleton className="h-3 w-16" />
      </div>
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-4 border-b last:border-b-0 px-4 py-3">
          <Skeleton className="h-3 w-6 rounded-full" />
          <Skeleton className="h-3 w-32" />
          <Skeleton className="h-3 w-20" />
          <Skeleton className="h-5 w-16 rounded-full" />
          <Skeleton className="h-3 w-8" />
        </div>
      ))}
    </div>
  )
}

/** Generic page-level loading skeleton */
export function PageSkeleton() {
  return (
    <div className="container mx-auto py-6 space-y-4">
      <div className="flex items-center justify-between">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-9 w-24 rounded-md" />
      </div>
      <div className="flex gap-2">
        <Skeleton className="h-9 w-56 rounded-md" />
        <Skeleton className="h-9 w-32 rounded-md" />
      </div>
      <div className="space-y-3">
        <Skeleton className="h-48 w-full rounded-lg" />
        <Skeleton className="h-48 w-full rounded-lg" />
      </div>
    </div>
  )
}
