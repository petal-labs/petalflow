import { useEffect, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useToolStore, useToolsByOrigin } from '@/stores/tool'
import { Icon } from '@/components/ui/icon'
import { ToolOriginBadge, StatusBadge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { Tool, ToolOrigin } from '@/lib/api-types'

export const Route = createFileRoute('/_app/tools/')({
  component: ToolsPage,
})

function toolStatusBadge(tool: Tool): 'ready' | 'unhealthy' | 'unverified' | 'disabled' {
  if (tool.status === 'ready') return 'ready'
  if (tool.status === 'unhealthy') return 'unhealthy'
  if (tool.status === 'unverified') return 'unverified'
  return 'disabled'
}

function ToolsPage() {
  const rawTools = useToolStore((s) => s.tools)
  const loading = useToolStore((s) => s.loading)
  const fetchTools = useToolStore((s) => s.fetchTools)
  const deleteTool = useToolStore((s) => s.deleteTool)
  const checkHealth = useToolStore((s) => s.checkHealth)
  const toolsByOrigin = useToolsByOrigin()

  // Defensive: ensure tools is always an array
  const tools = Array.isArray(rawTools) ? rawTools : []

  const [selectedTool, setSelectedTool] = useState<Tool | null>(null)
  const [showRegisterModal, setShowRegisterModal] = useState(false)
  const [filter, setFilter] = useState<ToolOrigin | 'all'>('all')

  useEffect(() => {
    fetchTools()
  }, [fetchTools])

  const filteredTools = filter === 'all'
    ? tools
    : tools.filter((t) => t.origin === filter)

  const handleHealthCheck = async (tool: Tool) => {
    try {
      const result = await checkHealth(tool.name)
      alert(`Health check: ${result.status}`)
    } catch {
      alert('Health check failed')
    }
  }

  const handleDelete = async (tool: Tool) => {
    if (confirm(`Delete tool "${tool.name}"?`)) {
      await deleteTool(tool.name)
      if (selectedTool?.name === tool.name) {
        setSelectedTool(null)
      }
    }
  }

  return (
    <div className="flex h-full overflow-hidden">
      {/* Main content */}
      <div className="flex-1 overflow-auto">
        <div className="p-7">
          <div className="flex items-center justify-between mb-6">
            <div>
              <h1 className="text-xl font-bold text-foreground">Tool Registry</h1>
              <p className="text-sm text-muted-foreground mt-1">
                Manage registered tools from native, MCP, HTTP, and STDIO sources
              </p>
            </div>
            <button
              onClick={() => setShowRegisterModal(true)}
              className={cn(
                'inline-flex items-center gap-1.5 rounded-lg font-semibold transition-all',
                'text-[13px] px-3.5 py-2',
                'bg-primary text-white hover:bg-primary/90'
              )}
            >
              <Icon name="plus" size={15} />
              Register Tool
            </button>
          </div>

          {/* Filter tabs */}
          <div className="flex items-center gap-2 mb-6">
            {(['all', 'native', 'mcp', 'http', 'stdio'] as const).map((origin) => (
              <button
                key={origin}
                onClick={() => setFilter(origin)}
                className={cn(
                  'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors',
                  filter === origin
                    ? 'bg-primary text-white'
                    : 'bg-surface-1 text-muted-foreground hover:text-foreground'
                )}
              >
                {origin === 'all' ? 'All' : origin.toUpperCase()}
                <span className="ml-1.5 text-xs opacity-70">
                  ({origin === 'all' ? tools.length : toolsByOrigin[origin].length})
                </span>
              </button>
            ))}
          </div>

          {loading ? (
            <div className="flex items-center justify-center h-64">
              <div className="animate-pulse text-sm text-muted-foreground">Loading tools...</div>
            </div>
          ) : filteredTools.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-64 text-center">
              <div className="w-14 h-14 mb-4 rounded-full bg-surface-2 flex items-center justify-center">
                <Icon name="tools" size={24} className="text-muted-foreground" />
              </div>
              <h3 className="text-lg font-semibold text-foreground mb-1">No tools registered</h3>
              <p className="text-sm text-muted-foreground max-w-sm">
                Register tools from MCP servers, HTTP endpoints, or STDIO executables to use them in workflows.
              </p>
            </div>
          ) : (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              {filteredTools.map((tool) => (
                <ToolCard
                  key={tool.name}
                  tool={tool}
                  selected={selectedTool?.name === tool.name}
                  onSelect={() => setSelectedTool(tool)}
                  onHealthCheck={() => handleHealthCheck(tool)}
                  onDelete={() => handleDelete(tool)}
                />
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Detail panel */}
      {selectedTool && (
        <div className="w-[380px] border-l border-border bg-surface-0 overflow-auto">
          <ToolDetail tool={selectedTool} onClose={() => setSelectedTool(null)} />
        </div>
      )}

      {/* Register modal */}
      {showRegisterModal && (
        <RegisterToolModal onClose={() => setShowRegisterModal(false)} />
      )}
    </div>
  )
}

interface ToolCardProps {
  tool: Tool
  selected: boolean
  onSelect: () => void
  onHealthCheck: () => void
  onDelete: () => void
}

function ToolCard({ tool, selected, onSelect, onHealthCheck, onDelete }: ToolCardProps) {
  return (
    <div
      onClick={onSelect}
      className={cn(
        'p-4 rounded-xl border bg-surface-0 cursor-pointer transition-all',
        selected ? 'border-primary ring-1 ring-primary' : 'border-border hover:border-primary/50'
      )}
    >
      <div className="flex items-start justify-between mb-3">
        <div className="flex items-center gap-2">
          <div className="w-9 h-9 rounded-lg bg-surface-1 flex items-center justify-center">
            <Icon name="tools" size={18} className="text-primary" />
          </div>
          <div>
            <div className="font-semibold text-sm text-foreground">{tool.name}</div>
            <div className="text-xs text-muted-foreground">{tool.manifest.version || 'v1.0'}</div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <ToolOriginBadge origin={tool.origin} />
          <StatusBadge status={toolStatusBadge(tool)} />
        </div>
      </div>

      {tool.manifest.description && (
        <p className="text-xs text-muted-foreground mb-3 line-clamp-2">
          {tool.manifest.description}
        </p>
      )}

      <div className="flex items-center justify-between">
        <div className="text-xs text-muted-foreground">
          {tool.manifest.actions.length} action{tool.manifest.actions.length !== 1 ? 's' : ''}
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={(e) => {
              e.stopPropagation()
              onHealthCheck()
            }}
            className="p-1.5 rounded-lg hover:bg-surface-1 text-muted-foreground hover:text-foreground transition-colors"
            title="Health check"
          >
            <Icon name="zap" size={14} />
          </button>
          <button
            onClick={(e) => {
              e.stopPropagation()
              onDelete()
            }}
            className="p-1.5 rounded-lg hover:bg-surface-1 text-muted-foreground hover:text-red transition-colors"
            title="Delete"
          >
            <Icon name="trash" size={14} />
          </button>
        </div>
      </div>
    </div>
  )
}

interface ToolDetailProps {
  tool: Tool
  onClose: () => void
}

function ToolDetail({ tool, onClose }: ToolDetailProps) {
  return (
    <div className="p-4">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-sm font-bold text-foreground">Tool Details</h2>
        <button
          onClick={onClose}
          className="p-1 text-muted-foreground hover:text-foreground transition-colors"
        >
          <Icon name="x" size={16} />
        </button>
      </div>

      <div className="space-y-4">
        {/* Header */}
        <div className="p-3 rounded-lg bg-surface-1 border border-border">
          <div className="flex items-center gap-2 mb-2">
            <ToolOriginBadge origin={tool.origin} />
            <StatusBadge status={toolStatusBadge(tool)} />
          </div>
          <div className="font-bold text-sm text-foreground">{tool.name}</div>
          {tool.manifest.description && (
            <p className="text-xs text-muted-foreground mt-1">{tool.manifest.description}</p>
          )}
        </div>

        {/* Metadata */}
        <div>
          <div className="text-xs font-semibold text-muted-foreground uppercase mb-2">
            Metadata
          </div>
          <div className="space-y-2 text-xs">
            <div className="flex justify-between">
              <span className="text-muted-foreground">Version</span>
              <span className="text-foreground">{tool.manifest.version || 'Unknown'}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Registered</span>
              <span className="text-foreground">
                {new Date(tool.registered_at).toLocaleDateString()}
              </span>
            </div>
            {tool.last_health_check && (
              <div className="flex justify-between">
                <span className="text-muted-foreground">Last health check</span>
                <span className="text-foreground">
                  {new Date(tool.last_health_check).toLocaleTimeString()}
                </span>
              </div>
            )}
          </div>
        </div>

        {/* Actions */}
        <div>
          <div className="text-xs font-semibold text-muted-foreground uppercase mb-2">
            Actions ({tool.manifest.actions.length})
          </div>
          <div className="space-y-2">
            {tool.manifest.actions.map((action) => (
              <div
                key={action.name}
                className="p-2.5 rounded-lg bg-surface-1 border border-border"
              >
                <div className="font-medium text-xs text-foreground">{action.name}</div>
                {action.description && (
                  <p className="text-[11px] text-muted-foreground mt-0.5">
                    {action.description}
                  </p>
                )}
                {action.parameters && Object.keys(action.parameters).length > 0 && (
                  <div className="mt-2 pt-2 border-t border-border">
                    <div className="text-[10px] text-muted-foreground uppercase mb-1">Parameters</div>
                    <pre className="text-[10px] text-muted-foreground font-mono overflow-auto max-h-20">
                      {JSON.stringify(action.parameters, null, 2)}
                    </pre>
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>

        {/* Config */}
        {tool.config && Object.keys(tool.config).length > 0 && (
          <div>
            <div className="text-xs font-semibold text-muted-foreground uppercase mb-2">
              Configuration
            </div>
            <pre className="p-2 rounded-lg bg-surface-1 border border-border text-[11px] text-foreground font-mono overflow-auto max-h-32">
              {JSON.stringify(tool.config, null, 2)}
            </pre>
          </div>
        )}
      </div>
    </div>
  )
}

interface RegisterToolModalProps {
  onClose: () => void
}

function RegisterToolModal({ onClose }: RegisterToolModalProps) {
  const registerTool = useToolStore((s) => s.registerTool)
  const [origin, setOrigin] = useState<ToolOrigin>('mcp')
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setIsSubmitting(true)
    setError(null)

    try {
      const trimmedName = name.trim()
      const target = endpoint.trim()
      if (!trimmedName) {
        throw new Error('Tool name is required')
      }
      if (!/^[a-z][a-z0-9_]{1,63}$/.test(trimmedName)) {
        throw new Error('Tool name must match ^[a-z][a-z0-9_]{1,63}$')
      }

      const actionDescription = description.trim() || `Execute ${trimmedName}`

      let transport: Record<string, unknown>
      if (origin === 'http') {
        if (!target) {
          throw new Error('HTTP endpoint is required')
        }
        transport = {
          type: 'http',
          endpoint: target,
        }
      } else if (origin === 'stdio') {
        if (!target) {
          throw new Error('Command is required')
        }
        const [command, ...args] = target.split(/\s+/).filter(Boolean)
        transport = {
          type: 'stdio',
          command,
          args,
        }
      } else {
        if (!target) {
          throw new Error('MCP target is required')
        }
        if (/^https?:\/\//i.test(target)) {
          transport = {
            type: 'mcp',
            mode: 'sse',
            endpoint: target,
          }
        } else {
          const [command, ...args] = target.split(/\s+/).filter(Boolean)
          transport = {
            type: 'mcp',
            mode: 'stdio',
            command,
            args,
          }
        }
      }

      await registerTool({
        name: trimmedName,
        type: origin,
        manifest: {
          manifest_version: '1.0',
          tool: {
            name: trimmedName,
            description: description.trim() || undefined,
          },
          transport,
          actions: {
            run: {
              description: actionDescription,
              inputs: {
                input: { type: 'object' },
              },
              outputs: {
                output: { type: 'object' },
              },
            },
          },
        },
      })
      onClose()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onClose}
    >
      <div
        className="bg-surface-0 rounded-xl shadow-lg border border-border p-6 w-full max-w-md"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex justify-between items-center mb-4">
          <h2 className="text-lg font-bold text-foreground">Register Tool</h2>
          <button
            onClick={onClose}
            className="p-1 text-muted-foreground hover:text-foreground transition-colors"
          >
            <Icon name="x" size={18} />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          {/* Origin */}
          <div>
            <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
              Origin
            </label>
            <div className="flex gap-2">
              {(['mcp', 'http', 'stdio'] as const).map((o) => (
                <button
                  key={o}
                  type="button"
                  onClick={() => setOrigin(o)}
                  className={cn(
                    'flex-1 px-3 py-2 rounded-lg text-sm font-medium transition-colors',
                    origin === o
                      ? 'bg-primary text-white'
                      : 'bg-surface-1 text-muted-foreground hover:text-foreground'
                  )}
                >
                  {o.toUpperCase()}
                </button>
              ))}
            </div>
          </div>

          {/* Name */}
          <div>
            <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
              Tool Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my_tool"
              required
              className={cn(
                'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                'text-foreground text-sm',
                'focus:outline-none focus:ring-1 focus:ring-primary'
              )}
            />
          </div>

          {/* Description */}
          <div>
            <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
              Description
            </label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What does this tool do?"
              rows={2}
              className={cn(
                'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                'text-foreground text-sm resize-none',
                'focus:outline-none focus:ring-1 focus:ring-primary'
              )}
            />
          </div>

          {/* Endpoint (for MCP/HTTP) */}
          {(origin === 'mcp' || origin === 'http') && (
            <div>
              <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
                {origin === 'mcp' ? 'MCP Server URL' : 'HTTP Endpoint'}
              </label>
              <input
                type="url"
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
                placeholder={origin === 'mcp' ? 'http://localhost:3000' : 'https://api.example.com/tool'}
                className={cn(
                  'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                  'text-foreground text-sm',
                  'focus:outline-none focus:ring-1 focus:ring-primary'
                )}
              />
            </div>
          )}

          {/* Command (for STDIO) */}
          {origin === 'stdio' && (
            <div>
              <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
                Command
              </label>
              <input
                type="text"
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
                placeholder="python3 my_tool.py"
                className={cn(
                  'w-full px-3 py-2 rounded-lg border border-border bg-surface-1',
                  'text-foreground text-sm',
                  'focus:outline-none focus:ring-1 focus:ring-primary'
                )}
              />
            </div>
          )}

          {error && (
            <div className="p-3 rounded-lg bg-red-soft text-red text-sm">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className={cn(
                'px-4 py-2 rounded-lg font-semibold text-sm transition-colors',
                'bg-transparent text-muted-foreground hover:text-foreground'
              )}
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isSubmitting || !name}
              className={cn(
                'inline-flex items-center gap-2 px-4 py-2 rounded-lg font-semibold text-sm transition-all',
                'bg-primary text-white hover:bg-primary/90',
                (isSubmitting || !name) && 'opacity-50 cursor-not-allowed'
              )}
            >
              {isSubmitting ? 'Registering...' : 'Register'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
