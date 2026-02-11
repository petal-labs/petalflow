import { useEffect, useRef } from "react"
import { useWorkflowStore } from "@/stores/workflows"

/**
 * Auto-saves the current workflow on edit with a 2-second debounce.
 * Also supports manual Ctrl+S.
 */
export function useAutoSave() {
  const dirty = useWorkflowStore((s) => s.dirty)
  const save = useWorkflowStore((s) => s.save)
  const current = useWorkflowStore((s) => s.current)
  const debounceRef = useRef<ReturnType<typeof globalThis.setTimeout> | null>(null)

  // Auto-save on dirty
  useEffect(() => {
    if (!dirty || !current) return

    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = globalThis.setTimeout(() => {
      save()
    }, 2000)

    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [dirty, save, current])

  // Ctrl+S manual save
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "s") {
        e.preventDefault()
        if (current) save()
      }
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [save, current])
}
