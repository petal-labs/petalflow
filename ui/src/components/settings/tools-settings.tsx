import { useEffect, useMemo, useState } from "react"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { RegisterToolSheet } from "@/components/tools/register-tool-sheet"
import { ToolDetailSheet } from "@/components/tools/tool-detail-sheet"
import { useToolStore } from "@/stores/tools"
import { toast } from "sonner"
import type { Tool, ToolStatus } from "@/api/types"

type StatusFilter = "all" | ToolStatus

const statusColors: Record<ToolStatus, string> = {
  ready: "bg-green-500",
  unhealthy: "bg-red-500",
  disabled: "bg-muted-foreground/40",
  unverified: "bg-yellow-500",
}

const statusBadgeClasses: Record<ToolStatus, string> = {
  ready: "bg-green-500/10 text-green-600",
  unhealthy: "bg-red-500/10 text-red-600",
  disabled: "bg-muted text-muted-foreground",
  unverified: "bg-yellow-500/10 text-yellow-600",
}

export function ToolsSettings() {
  const tools = useToolStore((s) => s.tools)
  const loading = useToolStore((s) => s.loading)
  const fetchTools = useToolStore((s) => s.fetchTools)
  const deleteTool = useToolStore((s) => s.deleteTool)
  const enableTool = useToolStore((s) => s.enableTool)
  const disableTool = useToolStore((s) => s.disableTool)

  const [search, setSearch] = useState("")
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all")
  const [registerOpen, setRegisterOpen] = useState(false)
  const [detailTool, setDetailTool] = useState<Tool | null>(null)

  useEffect(() => {
    fetchTools({ includeSchemas: true })
  }, [fetchTools])

  const filtered = useMemo(() => {
    let result = [...tools]
    if (search) {
      const q = search.toLowerCase()
      result = result.filter(
        (t) =>
          t.name.toLowerCase().includes(q) ||
          t.type.toLowerCase().includes(q) ||
          t.description?.toLowerCase().includes(q),
      )
    }
    if (statusFilter !== "all") {
      result = result.filter((t) => t.status === statusFilter)
    }
    return result
  }, [tools, search, statusFilter])

  const handleDelete = async (name: string) => {
    try {
      await deleteTool(name)
      toast.success(`Tool "${name}" removed.`)
    } catch {
      toast.error("Failed to remove tool.")
    }
  }

  const handleToggle = async (tool: Tool) => {
    try {
      if (tool.status === "disabled") {
        await enableTool(tool.name)
      } else {
        await disableTool(tool.name)
      }
    } catch {
      toast.error("Failed to update tool status.")
    }
  }

  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const t of tools) {
      counts[t.status] = (counts[t.status] ?? 0) + 1
    }
    return counts
  }, [tools])

  return (
    <div className="space-y-4 max-w-4xl">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-medium">Tool Registry</h2>
          <p className="text-sm text-muted-foreground">
            Manage external tools your agents can use.
          </p>
        </div>
        <Button size="sm" onClick={() => setRegisterOpen(true)}>
          + Register Tool
        </Button>
      </div>

      {/* Search & filter */}
      <div className="flex flex-wrap items-center gap-2">
        <Input
          placeholder="Search tools..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-xs"
        />
        <Select
          value={statusFilter}
          onValueChange={(v) => setStatusFilter(v as StatusFilter)}
        >
          <SelectTrigger className="w-[130px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Status</SelectItem>
            <SelectItem value="ready">Ready</SelectItem>
            <SelectItem value="unhealthy">Unhealthy</SelectItem>
            <SelectItem value="disabled">Disabled</SelectItem>
            <SelectItem value="unverified">Unverified</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Table */}
      {loading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : filtered.length === 0 ? (
        <div className="py-8 text-center text-sm text-muted-foreground">
          {tools.length === 0
            ? "No tools registered. Click \"+ Register Tool\" to get started."
            : "No tools match your search."}
        </div>
      ) : (
        <>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Transport</TableHead>
                  <TableHead>Actions</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((tool) => (
                  <TableRow
                    key={tool.name}
                    className="cursor-pointer"
                    onClick={() => setDetailTool(tool)}
                  >
                    <TableCell className="font-medium">{tool.name}</TableCell>
                    <TableCell className="text-muted-foreground">
                      {tool.type}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {tool.transport}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {tool.actions.length === 1
                        ? tool.actions[0].name
                        : `${tool.actions.length} actions`}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1.5">
                        <span
                          className={`inline-block h-2 w-2 rounded-full ${statusColors[tool.status]}`}
                        />
                        <Badge
                          variant="secondary"
                          className={`text-[10px] ${statusBadgeClasses[tool.status]}`}
                        >
                          {tool.status}
                        </Badge>
                      </div>
                    </TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-6 w-6 p-0"
                            onClick={(e) => e.stopPropagation()}
                          >
                            &middot;&middot;&middot;
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            onClick={(e) => {
                              e.stopPropagation()
                              handleToggle(tool)
                            }}
                          >
                            {tool.status === "disabled" ? "Enable" : "Disable"}
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            onClick={(e) => {
                              e.stopPropagation()
                              handleDelete(tool.name)
                            }}
                            className="text-destructive focus:text-destructive"
                          >
                            Remove
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          {/* Summary */}
          <p className="text-xs text-muted-foreground">
            Showing {filtered.length} tool{filtered.length !== 1 ? "s" : ""}
            {Object.entries(statusCounts).length > 0 &&
              ` (${Object.entries(statusCounts)
                .map(([s, c]) => `${c} ${s}`)
                .join(", ")})`}
          </p>
        </>
      )}

      {/* Registration sheet */}
      <RegisterToolSheet
        open={registerOpen}
        onOpenChange={setRegisterOpen}
      />

      {/* Detail sheet */}
      <ToolDetailSheet
        tool={detailTool}
        open={detailTool !== null}
        onOpenChange={(open) => {
          if (!open) setDetailTool(null)
        }}
      />
    </div>
  )
}
