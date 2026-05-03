// web/src/components/ConfirmDialog.tsx
import { Button, Modal } from './ui'

interface Props {
  message: string
  onConfirm: () => void
  onCancel: () => void
  /** Action label — defaults to "Delete". Use "Disconnect", "Remove", etc. */
  confirmLabel?: string
  /** Optional title rendered in the modal header. */
  title?: string
}

/**
 * ConfirmDialog — destructive-action confirmation modal.
 * Built on the design-system Modal so visuals stay consistent across
 * Secrets, Repos, Users, and any future page that needs one.
 */
export function ConfirmDialog({
  message,
  onConfirm,
  onCancel,
  confirmLabel = 'Delete',
  title,
}: Props) {
  return (
    <Modal title={title} onClose={onCancel}>
      <Modal.Body>
        <p className="m-0 text-[13px] leading-[1.55] text-ink-2">{message}</p>
      </Modal.Body>
      <Modal.Actions>
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button onClick={onConfirm}>{confirmLabel}</Button>
      </Modal.Actions>
    </Modal>
  )
}
