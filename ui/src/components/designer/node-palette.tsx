import { useCallback, useEffect, useMemo, useState } from "react"
import { Input } from "@/components/ui/input"
import { useGraphStore, makeGraphNodeId, type GraphNodeData, type PortHandle } from "@/stores/graph"
import { useToolStore } from "@/stores/tools"
import type { NodeType, Tool } from "@/api/types"
import { api } from "@/api/client"

interface PaletteItem {
  kind: string
  category: string
  label: string
  description?: string
  inputPorts: PortHandle[]
  outputPorts: PortHandle[]
  /** For multi-action tools, the available actions. */
  actions?: string[]
}

/** Category display order. */
const categoryOrder = ["LLM", "Processing", "Tools", "Control Flow"]

const categoryIcons: Record<string, string> = {
  LLM: "\u{1F9E0}",
  Processing: "\u2699\uFE0F",
  Tools: "\u{1F527}",
  "Control Flow": "\u{1F500}",
}

/** Convert built-in node types to palette items. */
function nodeTypeToPaletteItem(nt: NodeType): PaletteItem {
  return {
    kind: nt.kind,
    category: nt.category,
    label: nt.kind.replace(/_/g, " "),
    description: nt.description,
    inputPorts: nt.input_ports.map((p) => ({
      name: p.name,
      type: p.type,
      required: p.required,
    })),
    outputPorts: nt.output_ports.map((p) => ({
      name: p.name,
      type: p.type,
    })),
  }
}

/** Convert a registered tool to palette items (one per action or one for the tool). */
function toolToPaletteItems(tool: Tool): PaletteItem[] {
  if (tool.actions.length <= 1) {
    const action = tool.actions[0]
    return [
      {
        kind: `${tool.name}${action ? `.${action.name}` : ""}`,
        category: "Tools",
        label: tool.name,
        description: action?.description ?? tool.description,
        inputPorts: [{ name: "input", type: "string" }],
        outputPorts: [{ name: "output", type: "string" }],
      },
    ]
  }
  // Multi-action: single item with actions list
  return [
    {
      kind: tool.name,
      category: "Tools",
      label: tool.name,
      description: tool.description,
      inputPorts: [{ name: "input", type: "string" }],
      outputPorts: [{ name: "output", type: "string" }],
      actions: tool.actions.map((a) => a.name),
    },
  ]
}

export function NodePalette() {
  const addNode = useGraphStore((s) => s.addNode)
  const tools = useToolStore((s) => s.tools)
  const fetchTools = useToolStore((s) => s.fetchTools)

  const [nodeTypes, setNodeTypes] = useState<NodeType[]>([])
  const [search, setSearch] = useState("")
  const [expandedAction, setExpandedAction] = useState<string | null>(null)

  // Fetch node types on mount
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const res = await api.get<NodeType[]>("/api/node-types")
        if (!cancelled) setNodeTypes(res)
      } catch {
        // Endpoint may not exist yet — use defaults
        if (!cancelled) {
          setNodeTypes([
            {
              kind: "llm_prompt",
              category: "LLM",
              description: "Send a prompt to an LLM provider",
              input_ports: [
                { name: "input", type: "string", required: true },
                { name: "context", type: "string" },
              ],
              output_ports: [{ name: "output", type: "string" }],
            },
            {
              kind: "template_render",
              category: "Processing",
              description: "Render a Go text/template",
              input_ports: [{ name: "input", type: "string", required: true }],
              output_ports: [{ name: "output", type: "string" }],
            },
            {
              kind: "json_parse",
              category: "Processing",
              description: "Parse JSON string into object",
              input_ports: [{ name: "input", type: "string", required: true }],
              output_ports: [{ name: "output", type: "object" }],
            },
            {
              kind: "json_format",
              category: "Processing",
              description: "Format object as JSON string",
              input_ports: [{ name: "input", type: "object", required: true }],
              output_ports: [{ name: "output", type: "string" }],
            },
            {
              kind: "conditional",
              category: "Control Flow",
              description: "Branch based on expression",
              input_ports: [{ name: "input", type: "any", required: true }],
              output_ports: [
                { name: "true", type: "any" },
                { name: "false", type: "any" },
              ],
            },
            {
              kind: "gate",
              category: "Control Flow",
              description: "Human review gate",
              input_ports: [{ name: "input", type: "any", required: true }],
              output_ports: [{ name: "output", type: "any" }],
            },
          ])
        }
      }
    })()
    fetchTools({ status: "ready" })
    return () => {
      cancelled = true
    }
  }, [fetchTools])

  // Build palette items
  const items = useMemo(() => {
    const builtIn = nodeTypes.map(nodeTypeToPaletteItem)
    const toolItems = tools
      .filter((t) => t.status === "ready")
      .flatMap(toolToPaletteItems)
    return [...builtIn, ...toolItems]
  }, [nodeTypes, tools])

  // Filter by search
  const filtered = useMemo(() => {
    if (!search.trim()) return items
    const q = search.toLowerCase()
    return items.filter(
      (it) =>
        it.label.toLowerCase().includes(q) ||
        it.kind.toLowerCase().includes(q) ||
        it.category.toLowerCase().includes(q),
    )
  }, [items, search])

  // Group by category
  const grouped = useMemo(() => {
    const groups: Record<string, PaletteItem[]> = {}
    for (const item of filtered) {
      ;(groups[item.category] ??= []).push(item)
    }
    return groups
  }, [filtered])

  const handleAddNode = useCallback(
    (item: PaletteItem, action?: string) => {
      const kind = action ? `${item.kind}.${action}` : item.kind
      const id = makeGraphNodeId(kind.replace(/\./g, "_"))
      const data: GraphNodeData = {
        label: id,
        kind,
        category: item.category,
        config: action ? { action } : {},
        inputPorts: item.inputPorts,
        outputPorts: item.outputPorts,
      }
      // Place near center with some randomness to avoid stacking
      const x = 200 + Math.random() * 200
      const y = 100 + Math.random() * 200
      addNode({ id, type: "graphNode", position: { x, y }, data })
      setExpandedAction(null)
    },
    [addNode],
  )

  const handleDragStart = useCallback(
    (e: React.DragEvent, item: PaletteItem) => {
      e.dataTransfer.setData(
        "application/petalflow-node",
        JSON.stringify(item),
      )
      e.dataTransfer.effectAllowed = "move"
    },
    [],
  )

  return (
    <div className="flex h-full flex-col">
      <div className="border-b px-3 py-2">
        <span className="text-xs font-medium">Node Palette</span>
        <Input
          placeholder="Search..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="mt-1.5 h-7 text-xs"
        />
      </div>
      <div className="flex-1 overflow-y-auto p-2 space-y-3">
        {categoryOrder
          .filter((cat) => grouped[cat])
          .map((cat) => (
            <div key={cat}>
              <div className="flex items-center gap-1.5 px-1 py-1 text-[10px] font-semibold uppercase text-muted-foreground">
                <span>{categoryIcons[cat]}</span>
                <span>{cat}</span>
              </div>
              <div className="space-y-0.5">
                {grouped[cat].map((item) => (
                  <div key={item.kind}>
                    <button
                      type="button"
                      className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-xs hover:bg-muted/50 cursor-grab active:cursor-grabbing"
                      draggable
                      onDragStart={(e) => handleDragStart(e, item)}
                      onClick={() => {
                        if (item.actions && item.actions.length > 1) {
                          setExpandedAction(
                            expandedAction === item.kind ? null : item.kind,
                          )
                        } else {
                          handleAddNode(item)
                        }
                      }}
                      title={item.description}
                    >
                      <span className="truncate font-medium">
                        {item.label}
                      </span>
                    </button>
                    {/* Multi-action picker */}
                    {item.actions &&
                      expandedAction === item.kind && (
                        <div className="ml-4 space-y-0.5 border-l pl-2">
                          {item.actions.map((action) => (
                            <button
                              key={action}
                              type="button"
                              className="flex w-full items-center gap-1 rounded px-2 py-1 text-[11px] hover:bg-muted/50"
                              onClick={() => handleAddNode(item, action)}
                            >
                              <span className="text-muted-foreground">&bull;</span>
                              <span>{action}</span>
                            </button>
                          ))}
                        </div>
                      )}
                  </div>
                ))}
              </div>
            </div>
          ))}
        {/* Uncategorized */}
        {Object.entries(grouped)
          .filter(([cat]) => !categoryOrder.includes(cat))
          .map(([cat, catItems]) => (
            <div key={cat}>
              <div className="px-1 py-1 text-[10px] font-semibold uppercase text-muted-foreground">
                {cat}
              </div>
              <div className="space-y-0.5">
                {catItems.map((item) => (
                  <button
                    key={item.kind}
                    type="button"
                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-xs hover:bg-muted/50 cursor-grab active:cursor-grabbing"
                    draggable
                    onDragStart={(e) => handleDragStart(e, item)}
                    onClick={() => handleAddNode(item)}
                    title={item.description}
                  >
                    <span className="truncate font-medium">{item.label}</span>
                  </button>
                ))}
              </div>
            </div>
          ))}
      </div>
    </div>
  )
}
