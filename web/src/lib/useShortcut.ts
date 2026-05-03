import { useEffect } from 'react'

/**
 * Registers a global `keydown` listener that fires `handler` when `key`
 * is pressed (case-insensitive) outside of typing contexts (input/textarea/contenteditable).
 *
 * Removes the listener on unmount; safe to call from any page.
 */
export function useShortcut(key: string, handler: () => void) {
  useEffect(() => {
    const lower = key.toLowerCase()
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== lower) return
      // Don't steal browser/OS chord shortcuts (Cmd+N, Ctrl+N, Alt+N).
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const t = e.target as HTMLElement | null
      if (t) {
        const tag = t.tagName?.toLowerCase()
        if (tag === 'input' || tag === 'textarea' || t.isContentEditable) return
      }
      e.preventDefault()
      handler()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [key, handler])
}
