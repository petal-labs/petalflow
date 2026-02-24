import { useCallback, useMemo, useEffect } from 'react'
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  useNodesState,
  useEdgesState,
  addEdge,
  type Node,
  type Edge,
  type Connection,
  type NodeTypes,
  Handle,
  Position,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useWorkflowStore } from '@/stores/workflow'
import { useUIStore } from '@/stores/ui'
import { useProviderStore, DEFAULT_MODELS, PROVIDER_NAMES } from '@/stores/provider'
import { Badge } from '@/components/ui/badge'
import { FormInput } from './form-input'
import { cn } from '@/lib/utils'
import type { GraphDefinition, Provider, ProviderType } from '@/lib/api-types'

// Node data type with index signature for React Flow compatibility
type NodeData = {
  label: string
  nodeType?: string
  config?: Record<string, unknown>
  [key: string]: unknown
}

function buildProviderTypeOptions(providers: Provider[]): { value: string; label: string }[] {
  const configuredByType = new Map<string, Provider[]>()
  for (const provider of providers) {
    if (!provider.type) {
      continue
    }
    const existing = configuredByType.get(provider.type) || []
    existing.push(provider)
    configuredByType.set(provider.type, existing)
  }

  const knownProviderTypes = Object.keys(PROVIDER_NAMES) as ProviderType[]
  const allTypes = new Set<string>(knownProviderTypes)
  for (const provider of providers) {
    if (provider.type) {
      allTypes.add(provider.type)
    }
  }

  const options: { value: string; label: string }[] = []

  for (const providerType of allTypes) {
    const configured = configuredByType.get(providerType) || []
    const providerLabel =
      PROVIDER_NAMES[providerType as ProviderType] || providerType
    let suffix = ''
    if (configured.length === 1 && configured[0].name) {
      suffix = ` (${configured[0].name})`
    } else if (configured.length > 1) {
      suffix = ` (${configured.length} configured)`
    }
    options.push({
      value: providerType,
      label: `${providerLabel}${suffix}`,
    })
  }

  return options
}

function buildModelOptions(
  providerType: string,
  providers: Provider[],
  selectedModel = ''
): { value: string; label: string }[] {
  if (!providerType) {
    if (!selectedModel) {
      return []
    }
    return [{ value: selectedModel, label: selectedModel }]
  }

  const modelSet = new Set<string>()
  const knownModels = DEFAULT_MODELS[providerType as ProviderType] || []

  for (const model of knownModels) {
    if (model) {
      modelSet.add(model)
    }
  }

  for (const provider of providers) {
    if (provider.type === providerType && provider.default_model) {
      modelSet.add(provider.default_model)
    }
  }

  if (selectedModel) {
    modelSet.add(selectedModel)
  }

  return Array.from(modelSet).map((model) => ({
    value: model,
    label: model,
  }))
}

function nodeTypeColor(nodeType?: string): string {
  if (!nodeType) {
    return 'hsl(var(--primary))'
  }
  if (nodeType.startsWith('llm_')) {
    return 'var(--teal)'
  }
  if (nodeType.includes('router') || nodeType === 'conditional' || nodeType === 'gate' || nodeType === 'human') {
    return 'var(--orange)'
  }
  if (nodeType === 'transform' || nodeType === 'filter' || nodeType === 'merge' || nodeType === 'map' || nodeType === 'cache') {
    return 'var(--green)'
  }
  if (nodeType.startsWith('webhook_')) {
    return 'var(--blue)'
  }
  return 'hsl(var(--primary))'
}

// Custom node component for graph workflow
function GraphNode({ data, selected }: { data: NodeData; selected: boolean }) {
  const borderColor = nodeTypeColor(data.nodeType)

  return (
    <div
      className={cn(
        'px-5 py-2.5 rounded-[10px] min-w-[120px] bg-surface-1 shadow-lg',
        'flex flex-col items-center gap-1 cursor-pointer transition-all',
        selected && 'ring-2 ring-primary'
      )}
      style={{ border: `2px solid ${borderColor}` }}
    >
      <Handle
        type="target"
        position={Position.Left}
        className="w-3 h-3 !bg-muted-foreground !border-2 !border-surface-1"
      />

      <div className="text-xs font-bold" style={{ color: borderColor }}>
        {data.label}
      </div>
      {data.nodeType && (
        <div className="flex gap-1">
          <span
            className="w-1.5 h-1.5 rounded-full"
            style={{ backgroundColor: borderColor }}
          />
        </div>
      )}

      <Handle
        type="source"
        position={Position.Right}
        className="w-3 h-3 !border-2 !border-surface-1"
        style={{ backgroundColor: borderColor }}
      />
    </div>
  )
}

const nodeTypes: NodeTypes = {
  graphNode: GraphNode,
}

export function GraphDesigner() {
  const activeWorkflow = useWorkflowStore((s) => s.activeWorkflow)
  const activeSource = useWorkflowStore((s) => s.activeSource)
  const setActiveSource = useWorkflowStore((s) => s.setActiveSource)
  const selectedNodeId = useUIStore((s) => s.selectedNodeId)
  const selectNode = useUIStore((s) => s.selectNode)
  const nodeTypesFromApi = useUIStore((s) => s.nodeTypes)
  const fetchNodeTypes = useUIStore((s) => s.fetchNodeTypes)
  const rawProviders = useProviderStore((s) => s.providers)
  const fetchProviders = useProviderStore((s) => s.fetchProviders)
  const providers = useMemo(() => (Array.isArray(rawProviders) ? rawProviders : []), [rawProviders])

  // Fetch node types on mount
  useEffect(() => {
    fetchNodeTypes()
  }, [fetchNodeTypes])

  useEffect(() => {
    void fetchProviders()
  }, [fetchProviders])

  // Parse source into graph definition
  // Falls back to compiled field when source doesn't have nodes (common for pre-compiled workflows)
  const graphDef = useMemo((): GraphDefinition | null => {
    let parsed: GraphDefinition | null = null

    // Try to parse activeSource first
    if (activeSource) {
      try {
        if (typeof activeSource === 'string') {
          parsed = JSON.parse(activeSource) as GraphDefinition
        } else if (typeof activeSource === 'object') {
          parsed = activeSource as unknown as GraphDefinition
        }
      } catch {
        parsed = null
      }
    }

    // If parsed source has nodes, use it; otherwise fall back to compiled
    if (parsed?.nodes && parsed.nodes.length > 0) {
      return parsed
    }

    // Fall back to compiled field from activeWorkflow
    if (activeWorkflow?.compiled?.nodes && activeWorkflow.compiled.nodes.length > 0) {
      return activeWorkflow.compiled
    }

    return parsed
  }, [activeSource, activeWorkflow])

  // Convert graph definition to React Flow nodes/edges
  const initialNodes = useMemo((): Node[] => {
    if (!graphDef?.nodes) return []
    return graphDef.nodes.map((node, idx) => ({
      id: node.id,
      type: 'graphNode',
      position: { x: 100 + idx * 200, y: 100 + (idx % 2) * 80 },
      data: {
        label: node.id,
        nodeType: node.type,
        config: node.config,
      } as NodeData,
    }))
  }, [graphDef])

  const initialEdges = useMemo((): Edge[] => {
    if (!graphDef?.edges) return []
    return graphDef.edges.map((edge, idx) => ({
      id: `e${idx}`,
      source: edge.source,
      target: edge.target,
      sourceHandle: edge.source_handle || null,
      targetHandle: edge.target_handle || null,
      animated: true,
      style: { stroke: 'hsl(var(--primary))', strokeDasharray: '6 4' },
    }))
  }, [graphDef])

  const [nodes, setNodes, onNodesChange] = useNodesState(initialNodes)
  const [edges, setEdges, onEdgesChange] = useEdgesState(initialEdges)

  // Sync nodes/edges when activeSource changes (when a different workflow is selected)
  useEffect(() => {
    setNodes(initialNodes)
    setEdges(initialEdges)
  }, [activeSource, initialNodes, initialEdges, setNodes, setEdges])

  // Keep workflow source in sync with graph structure edits.
  useEffect(() => {
    if (!activeWorkflow || activeWorkflow.kind !== 'graph') {
      return
    }

    const fallbackID = graphDef?.id || activeWorkflow.id
    const fallbackVersion = graphDef?.version || '1.0'
    const fallbackMetadata = graphDef?.metadata
    const nextNodes = nodes.map((node) => ({
      id: node.id,
      type: (node.data as NodeData).nodeType || 'noop',
      config: ((node.data as NodeData).config || {}) as Record<string, unknown>,
    }))
    const nextEdges = edges.map((edge) => ({
      source: edge.source,
      source_handle: edge.sourceHandle || '',
      target: edge.target,
      target_handle: edge.targetHandle || '',
    }))
    const nodeIDs = new Set(nextNodes.map((node) => node.id))

    let entry = graphDef?.entry || nextNodes[0]?.id || ''
    if (entry && !nodeIDs.has(entry)) {
      entry = nextNodes[0]?.id || ''
    }

    const nextGraph: GraphDefinition = {
      id: fallbackID,
      version: fallbackVersion,
      metadata: fallbackMetadata,
      nodes: nextNodes,
      edges: nextEdges,
      entry,
    }

    const currentGraph: GraphDefinition = {
      id: graphDef?.id || fallbackID,
      version: graphDef?.version || fallbackVersion,
      metadata: graphDef?.metadata,
      nodes: (graphDef?.nodes || []).map((node) => ({
        id: node.id,
        type: node.type,
        config: node.config || {},
      })),
      edges: (graphDef?.edges || []).map((edge) => ({
        source: edge.source,
        source_handle: edge.source_handle || '',
        target: edge.target,
        target_handle: edge.target_handle || '',
      })),
      entry: graphDef?.entry || entry,
    }

    if (JSON.stringify(currentGraph) !== JSON.stringify(nextGraph)) {
      setActiveSource(JSON.stringify(nextGraph, null, 2))
    }
  }, [activeWorkflow, edges, graphDef, nodes, setActiveSource])

  const onConnect = useCallback(
    (connection: Connection) => {
      setEdges((eds) =>
        addEdge(
          {
            ...connection,
            animated: true,
            style: { stroke: 'hsl(var(--primary))', strokeDasharray: '6 4' },
          },
          eds
        )
      )
    },
    [setEdges]
  )

  const onNodeClick = useCallback(
    (_: React.MouseEvent, node: Node) => {
      selectNode(node.id)
    },
    [selectNode]
  )

  const onPaneClick = useCallback(() => {
    selectNode(null)
  }, [selectNode])

  // Find selected node data
  const selectedNode = nodes.find((n) => n.id === selectedNodeId)
  const selectedNodeData = selectedNode?.data as NodeData | undefined

  // Node palette for drag and drop
  const nodePalette = useMemo(() => {
    if (nodeTypesFromApi.length > 0) {
      const categories: Record<string, { type: string; label: string }[]> = {}
      for (const nt of nodeTypesFromApi) {
        const key = nt.category || 'Other'
        if (!categories[key]) {
          categories[key] = []
        }
        categories[key].push({
          type: nt.type,
          label: nt.display_name || nt.type,
        })
      }
      return categories
    }

    return {
      Core: [
        { type: 'noop', label: 'No-Op' },
        { type: 'llm_prompt', label: 'LLM Prompt' },
        { type: 'rule_router', label: 'Rule Router' },
      ],
      Data: [
        { type: 'transform', label: 'Transform' },
        { type: 'filter', label: 'Filter' },
        { type: 'merge', label: 'Merge' },
      ],
      Control: [
        { type: 'conditional', label: 'Conditional' },
        { type: 'human', label: 'Human' },
      ],
      Triggers: [
        { type: 'webhook_trigger', label: 'Webhook Trigger' },
      ],
      Outputs: [
        { type: 'webhook_call', label: 'Webhook Call' },
      ],
      Tools: [
        { type: 'tool', label: 'Tool' },
      ],
    } as Record<string, { type: string; label: string }[]>
  }, [nodeTypesFromApi])

  const addNode = useCallback(
    (type: string, label: string) => {
      const newNode: Node = {
        id: `node_${Date.now()}`,
        type: 'graphNode',
        position: { x: 200 + Math.random() * 100, y: 150 + Math.random() * 100 },
        data: { label, nodeType: type, config: {} } as NodeData,
      }
      setNodes((nds) => [...nds, newNode])
    },
    [setNodes]
  )

  // Check if selected node is an LLM prompt type
  const isLlmPrompt = selectedNodeData?.nodeType === 'llm_prompt'
  const selectedProviderType = (selectedNodeData?.config?.provider as string) || ''
  const providerOptions = useMemo(() => buildProviderTypeOptions(providers), [providers])
  const modelOptions = useMemo(
    () =>
      buildModelOptions(
        selectedProviderType,
        providers,
        (selectedNodeData?.config?.model as string) || ''
      ),
    [providers, selectedNodeData?.config?.model, selectedProviderType]
  )

  const updateSelectedNodeConfig = useCallback(
    (updates: Record<string, unknown>) => {
      if (!selectedNodeId) {
        return
      }
      setNodes((currentNodes) =>
        currentNodes.map((node) => {
          if (node.id !== selectedNodeId) {
            return node
          }
          const currentData = node.data as NodeData
          const currentConfig = currentData.config || {}
          return {
            ...node,
            data: {
              ...currentData,
              config: {
                ...currentConfig,
                ...updates,
              },
            } as NodeData,
          }
        })
      )
    },
    [selectedNodeId, setNodes]
  )

  return (
    <div className="flex h-full overflow-hidden">
      {/* Left: Node Palette */}
      <div className="w-52 border-r border-border bg-surface-0 overflow-auto">
        <div className="p-3 border-b border-border">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
            Node Palette
          </span>
        </div>
        <div className="p-3">
          {Object.entries(nodePalette).map(([category, items]) => (
            <div key={category} className="mb-4">
              <div className="text-[11px] font-semibold text-muted-foreground uppercase mb-2">
                {category}
              </div>
              <div className="space-y-1.5">
                {items.map((item) => (
                  <button
                    key={item.type}
                    onClick={() => addNode(item.type, item.label)}
                    className={cn(
                      'w-full text-left px-3 py-2 rounded-lg text-xs font-medium',
                      'bg-surface-1 border border-border',
                      'hover:border-primary/50 transition-colors'
                    )}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Center: Canvas */}
      <div className="flex-1 bg-canvas">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onNodeClick={onNodeClick}
          onPaneClick={onPaneClick}
          nodeTypes={nodeTypes}
          fitView
          snapToGrid
          snapGrid={[24, 24]}
          className="bg-canvas"
        >
          <Background gap={24} size={1} color="var(--border)" />
          <Controls className="!bg-surface-0 !border-border !shadow-lg" />
          <MiniMap
            nodeColor={(n) => nodeTypeColor((n.data as NodeData).nodeType)}
            className="!bg-surface-0/80 !border-border"
          />
        </ReactFlow>
      </div>

      {/* Right: Inspector */}
      <div className="w-[300px] border-l border-border bg-surface-0 overflow-auto">
        <div className="p-4 border-b border-border">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
            Node Inspector
          </span>
        </div>
        {selectedNodeData ? (
          <div className="p-4">
            <div
              className={cn(
                'p-3.5 rounded-[10px] mb-4',
                'bg-accent-soft border border-primary'
              )}
            >
              <div className="font-bold text-sm text-foreground mb-1">
                {selectedNodeData.label}
              </div>
              <Badge variant={selectedNodeData.nodeType === 'mcp' ? 'mcp' : 'native'}>
                {selectedNodeData.nodeType || 'native'}
              </Badge>
            </div>

            <FormInput
              label="Node ID"
              value={selectedNodeId || ''}
              disabled
            />

            {isLlmPrompt && (
              <>
                <FormInput
                  label="Provider"
                  value={(selectedNodeData.config?.provider as string) || ''}
                  onChange={(providerType) => {
                    const firstModel = buildModelOptions(providerType, providers)[0]?.value || ''
                    updateSelectedNodeConfig({
                      provider: providerType,
                      model: firstModel,
                    })
                  }}
                  type="select"
                  options={providerOptions}
                  placeholder="Select provider"
                  hint={
                    providers.length === 0
                      ? 'No providers configured. Add one on the Providers page.'
                      : undefined
                  }
                />
                <FormInput
                  label="Model"
                  value={(selectedNodeData.config?.model as string) || ''}
                  onChange={(model) => updateSelectedNodeConfig({ model })}
                  type="select"
                  options={modelOptions}
                  placeholder="Select model"
                  hint={
                    selectedProviderType && modelOptions.length === 0
                      ? 'No models found for this provider type.'
                      : undefined
                  }
                />
                <FormInput
                  label="System Prompt"
                  value={(selectedNodeData.config?.system_prompt as string) || ''}
                  type="textarea"
                  onChange={(systemPrompt) => updateSelectedNodeConfig({ system_prompt: systemPrompt })}
                />
                <FormInput
                  label="Temperature"
                  value={String(selectedNodeData.config?.temperature || 0.7)}
                  type="number"
                  onChange={(value) => {
                    const parsed = Number.parseFloat(value)
                    updateSelectedNodeConfig({
                      temperature: Number.isFinite(parsed) ? parsed : 0.7,
                    })
                  }}
                />
              </>
            )}

            <div className="mt-4 pt-4 border-t border-border">
              <div className="text-xs font-semibold text-muted-foreground mb-3">
                Ports
              </div>
              <div className="flex gap-2 flex-wrap">
                <span className="px-2 py-1 rounded text-[11px] bg-muted text-muted-foreground">
                  ← input
                </span>
                <span className="px-2 py-1 rounded text-[11px] bg-accent-soft text-primary">
                  output →
                </span>
              </div>
            </div>
          </div>
        ) : (
          <div className="flex items-center justify-center h-48 text-sm text-muted-foreground">
            Select a node to configure
          </div>
        )}
      </div>
    </div>
  )
}
