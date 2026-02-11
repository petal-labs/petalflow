import { useEffect, useState } from "react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"

interface ShortcutEntry {
  keys: string[]
  description: string
}

interface ShortcutGroup {
  title: string
  shortcuts: ShortcutEntry[]
}

const shortcutGroups: ShortcutGroup[] = [
  {
    title: "General",
    shortcuts: [
      { keys: ["?"], description: "Open keyboard shortcuts" },
      { keys: ["Ctrl", "S"], description: "Save workflow" },
      { keys: ["Ctrl", "K"], description: "Quick search" },
    ],
  },
  {
    title: "Designer — Graph Mode",
    shortcuts: [
      { keys: ["Ctrl", "Z"], description: "Undo" },
      { keys: ["Ctrl", "Shift", "Z"], description: "Redo" },
      { keys: ["Ctrl", "C"], description: "Copy selected nodes" },
      { keys: ["Ctrl", "V"], description: "Paste nodes" },
      { keys: ["Ctrl", "A"], description: "Select all nodes" },
      { keys: ["Delete"], description: "Delete selected (with confirmation)" },
      { keys: ["Backspace"], description: "Delete selected (with confirmation)" },
      { keys: ["Escape"], description: "Deselect all" },
    ],
  },
  {
    title: "Designer — Agent/Task Mode",
    shortcuts: [
      { keys: ["Ctrl", "Shift", "A"], description: "Add agent" },
      { keys: ["Ctrl", "Shift", "T"], description: "Add task" },
    ],
  },
  {
    title: "Runner",
    shortcuts: [
      { keys: ["Escape"], description: "Close modal / cancel" },
      { keys: ["Ctrl", "Enter"], description: "Start run" },
    ],
  },
  {
    title: "Library",
    shortcuts: [
      { keys: ["N"], description: "New workflow" },
      { keys: ["/"], description: "Focus search" },
    ],
  },
]

function Kbd({ children }: { children: string }) {
  return (
    <kbd className="inline-flex h-5 min-w-[20px] items-center justify-center rounded border border-border bg-muted px-1.5 text-[10px] font-medium text-muted-foreground">
      {children}
    </kbd>
  )
}

interface KeyboardShortcutsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function KeyboardShortcutsDialog({
  open,
  onOpenChange,
}: KeyboardShortcutsDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Keyboard Shortcuts</DialogTitle>
          <DialogDescription>
            Available shortcuts across the application.
          </DialogDescription>
        </DialogHeader>
        <ScrollArea className="max-h-[60vh]">
          <div className="space-y-4 pr-3">
            {shortcutGroups.map((group, gi) => (
              <div key={group.title}>
                {gi > 0 && <Separator className="mb-4" />}
                <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">
                  {group.title}
                </h4>
                <div className="space-y-1.5">
                  {group.shortcuts.map((shortcut) => (
                    <div
                      key={shortcut.description}
                      className="flex items-center justify-between py-0.5"
                    >
                      <span className="text-xs">{shortcut.description}</span>
                      <div className="flex items-center gap-0.5">
                        {shortcut.keys.map((key, ki) => (
                          <span key={ki} className="flex items-center gap-0.5">
                            {ki > 0 && (
                              <span className="text-[10px] text-muted-foreground mx-0.5">
                                +
                              </span>
                            )}
                            <Kbd>{key}</Kbd>
                          </span>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </ScrollArea>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Hook that listens for the `?` key to open the shortcuts dialog.
 * Returns [open, setOpen] state for the dialog.
 */
export function useShortcutsDialog(): [boolean, (open: boolean) => void] {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      // Don't trigger inside inputs/textareas/contenteditable
      const target = e.target as HTMLElement
      if (
        target.tagName === "INPUT" ||
        target.tagName === "TEXTAREA" ||
        target.tagName === "SELECT" ||
        target.isContentEditable
      ) {
        return
      }
      if (e.key === "?" && !e.ctrlKey && !e.metaKey && !e.altKey) {
        e.preventDefault()
        setOpen(true)
      }
    }
    document.addEventListener("keydown", handleKeyDown)
    return () => document.removeEventListener("keydown", handleKeyDown)
  }, [])
  return [open, setOpen]
}

