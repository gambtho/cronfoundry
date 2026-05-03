import { useEffect, useId, useRef } from 'react'
import { cn } from '../../lib/cn'

/**
 * Modal — overlay primitive. Used wherever we need a focused
 * decision (delete confirmations, secret create/rotate).
 *
 *   <Modal title="Delete secret?" onClose={cancel}>
 *     <Modal.Body>...</Modal.Body>
 *     <Modal.Actions>
 *       <Button variant="ghost" onClick={cancel}>Cancel</Button>
 *       <Button onClick={confirm}>Delete</Button>
 *     </Modal.Actions>
 *   </Modal>
 *
 * Behavior:
 *   - Backdrop click + Escape close the modal (keyboard parity).
 *   - Initial focus moves to the dialog and Tab/Shift-Tab cycle within
 *     it (focus trap), so keyboard users can't tab into background
 *     UI that's already aria-hidden by aria-modal.
 *   - role="dialog" + aria-modal so AT treats the rest of the page
 *     as inert.
 *   - Each instance generates a unique title id via useId so stacked
 *     modals don't collide on aria-labelledby.
 */
interface ModalProps {
  title?: React.ReactNode
  onClose: () => void
  children: React.ReactNode
  /** Set false to disable backdrop-click close (e.g. mid-flow forms). */
  dismissOnBackdropClick?: boolean
  className?: string
}

// Selector list copied from the WAI-ARIA-practices reference for
// "tabbable elements". Excludes negative-tabindex items.
const FOCUSABLE_SELECTOR = [
  'a[href]',
  'area[href]',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'button:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

export function Modal({
  title,
  onClose,
  children,
  dismissOnBackdropClick = true,
  className,
}: ModalProps) {
  const dialogRef = useRef<HTMLDivElement>(null)
  const titleId = useId()

  // Escape closes the modal + Tab/Shift-Tab traps focus inside it.
  // Both behaviors share one keydown listener so we only attach once.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        onClose()
        return
      }
      if (e.key !== 'Tab') return
      const root = dialogRef.current
      if (!root) return
      const items = root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)
      if (items.length === 0) {
        // Nothing to focus — keep focus pinned on the dialog itself.
        e.preventDefault()
        root.focus()
        return
      }
      const first = items[0]
      const last = items[items.length - 1]
      const active = document.activeElement as HTMLElement | null
      // Wrap forward off the last element; wrap backward off the first
      // OR off the dialog itself (initial state, focus on the wrapper).
      if (e.shiftKey) {
        if (active === first || active === root) {
          e.preventDefault()
          last.focus()
        }
      } else if (active === last) {
        e.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  // Move focus into the dialog on mount so screen readers announce it
  // and the focus-trap has a known starting point.
  useEffect(() => {
    dialogRef.current?.focus()
  }, [])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4"
      onClick={dismissOnBackdropClick ? onClose : undefined}
      aria-hidden="false"
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={title ? titleId : undefined}
        tabIndex={-1}
        // Stop the backdrop's onClick from firing when clicks happen
        // on the dialog itself.
        onClick={(e) => e.stopPropagation()}
        className={cn(
          'w-full max-w-md rounded border border-rule bg-bg-2 shadow-2xl outline-none',
          className,
        )}
      >
        {title && (
          <header className="border-b border-rule px-5 py-3.5">
            <h2 id={titleId} className="m-0 text-[15px] font-semibold text-ink">
              {title}
            </h2>
          </header>
        )}
        {children}
      </div>
    </div>
  )
}

function ModalBody({ children }: { children: React.ReactNode }) {
  return <div className="flex flex-col gap-3 px-5 py-4">{children}</div>
}

function ModalActions({ children }: { children: React.ReactNode }) {
  return (
    <footer className="flex justify-end gap-2 border-t border-rule px-5 py-3">
      {children}
    </footer>
  )
}

Modal.Body = ModalBody
Modal.Actions = ModalActions
