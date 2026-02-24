import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useUIStore } from '@/stores/ui'
import { useWorkflowStore } from '@/stores/workflow'
import { useRunStore } from '@/stores/run'
import { Icon } from '@/components/ui/icon'
import { cn } from '@/lib/utils'

export function RunModal() {
  const isOpen = useUIStore((s) => s.runModalOpen)
  const closeModal = useUIStore((s) => s.closeRunModal)
  const activeWorkflow = useWorkflowStore((s) => s.activeWorkflow)
  const startRun = useRunStore((s) => s.startRun)
  const navigate = useNavigate()

  const [inputText, setInputText] = useState('AI agents in 2025')
  const [input, setInput] = useState('{}')
  const [options, setOptions] = useState({
    humanMode: 'strict' as 'strict' | 'auto_approve' | 'auto_reject',
    timeout: 300,
  })
  const [isRunning, setIsRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleRun = async () => {
    if (!activeWorkflow) return

    setIsRunning(true)
    setError(null)

    try {
      const parsedInput = JSON.parse(input) as Record<string, unknown>
      const normalizedInputText = inputText.trim()
      const runInput: Record<string, unknown> = { ...parsedInput }

      if (normalizedInputText !== '') {
        runInput.input_text = normalizedInputText
        if (typeof runInput.topic !== 'string' || runInput.topic.trim() === '') {
          runInput.topic = normalizedInputText
        }
      }

      const run = await startRun(activeWorkflow.id, runInput, {
        stream: true,
        timeoutSeconds: options.timeout,
        humanMode: options.humanMode,
      })
      closeModal()
      navigate({ to: '/runs', search: { viewRun: run.run_id } })
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setIsRunning(false)
    }
  }

  if (!isOpen) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={closeModal}
    >
      <div
        className="bg-surface-0 rounded-xl shadow-lg border border-border p-6 w-full max-w-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex justify-between items-center mb-4">
          <div>
            <h2 className="text-lg font-bold text-foreground">Run Workflow</h2>
            {activeWorkflow && (
              <p className="text-xs text-muted-foreground mt-0.5">
                {activeWorkflow.name}
              </p>
            )}
          </div>
          <button
            onClick={closeModal}
            className="p-1 text-muted-foreground hover:text-foreground transition-colors"
          >
            <Icon name="x" size={18} />
          </button>
        </div>

        {/* Input Text */}
        <div className="mb-4">
          <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
            Input Text
          </label>
          <textarea
            value={inputText}
            onChange={(e) => setInputText(e.target.value)}
            className={cn(
              'w-full h-20 px-3 py-2 rounded-lg border border-border bg-surface-1',
              'text-foreground text-sm',
              'focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary',
              'resize-none'
            )}
            placeholder="What should the workflow work on?"
          />
          <p className="mt-1 text-xs text-muted-foreground">
            Passed as <code>input_text</code>. If <code>topic</code> is not provided, it also populates <code>topic</code>.
          </p>
        </div>

        {/* Additional input JSON */}
        <div className="mb-4">
          <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
            Additional Inputs (JSON)
          </label>
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            className={cn(
              'w-full h-32 px-3 py-2 rounded-lg border border-border bg-surface-1',
              'text-foreground text-sm font-mono',
              'focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary',
              'resize-none'
            )}
            placeholder='{"key": "value"}'
          />
        </div>

        {/* Options */}
        <div className="mb-4 space-y-3">
          <label className="block text-xs font-semibold text-muted-foreground mb-2">
            Options
          </label>

          <div>
            <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
              Human Node Handling
            </label>
            <select
              value={options.humanMode}
              onChange={(e) =>
                setOptions((o) => ({
                  ...o,
                  humanMode: e.target.value as 'strict' | 'auto_approve' | 'auto_reject',
                }))
              }
              className={cn(
                'w-full px-2.5 py-2 rounded-lg border border-border bg-surface-1',
                'text-foreground text-sm',
                'focus:outline-none focus:ring-1 focus:ring-primary'
              )}
            >
              <option value="strict">strict (fail on human input requests)</option>
              <option value="auto_approve">auto_approve</option>
              <option value="auto_reject">auto_reject</option>
            </select>
          </div>

          <div>
            <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
              Timeout (s)
            </label>
            <input
              type="number"
              value={options.timeout}
              onChange={(e) =>
                setOptions((o) => ({
                  ...o,
                  timeout: parseInt(e.target.value, 10) || 300,
                }))
              }
              min={10}
              max={3600}
              className={cn(
                'w-full px-2.5 py-2 rounded-lg border border-border bg-surface-1',
                'text-foreground text-sm',
                'focus:outline-none focus:ring-1 focus:ring-primary'
              )}
            />
          </div>
        </div>

        {/* Error */}
        {error && (
          <div className="mb-4 p-3 rounded-lg bg-red-soft text-red text-sm">
            {error}
          </div>
        )}

        {/* Actions */}
        <div className="flex justify-end gap-2">
          <button
            onClick={closeModal}
            className={cn(
              'px-4 py-2 rounded-lg font-semibold text-sm transition-colors',
              'bg-transparent text-muted-foreground hover:text-foreground'
            )}
          >
            Cancel
          </button>
          <button
            onClick={handleRun}
            disabled={isRunning || !activeWorkflow}
            className={cn(
              'inline-flex items-center gap-2 px-4 py-2 rounded-lg font-semibold text-sm transition-all',
              'bg-primary text-white hover:bg-primary/90',
              (isRunning || !activeWorkflow) && 'opacity-50 cursor-not-allowed'
            )}
          >
            <Icon name="play" size={14} />
            {isRunning ? 'Starting...' : 'Run'}
          </button>
        </div>
      </div>
    </div>
  )
}
