// web/src/components/SecretModal.tsx
import { useState } from 'react'
import { Button, Input, Modal } from './ui'

interface Props {
  mode: 'create' | 'rotate'
  name?: string
  onSubmit: (name: string, value: string) => void
  onCancel: () => void
}

/**
 * SecretModal — create or rotate a secret. Built on the design-system
 * Modal + Input primitives so the form chrome matches the rest of
 * the app. Backdrop-click is disabled because losing a half-typed
 * secret value is annoying; users can still hit Cancel or Escape.
 */
export function SecretModal({
  mode,
  name: initialName = '',
  onSubmit,
  onCancel,
}: Props) {
  const [name, setName] = useState(initialName)
  const [value, setValue] = useState('')

  const canSubmit =
    (mode === 'rotate' || name.trim().length > 0) && value.length > 0

  return (
    <Modal
      title={mode === 'create' ? 'Add secret' : `Rotate ${initialName}`}
      onClose={onCancel}
      dismissOnBackdropClick={false}
    >
      <Modal.Body>
        {mode === 'create' && (
          <Input
            label="Name"
            placeholder="MY_API_KEY"
            value={name}
            onChange={(e) => setName(e.target.value)}
            variant="mono"
            autoFocus
          />
        )}
        <Input
          label="Value"
          type="password"
          placeholder="sk-…"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          hint="Value is never shown after saving."
          variant="mono"
          autoFocus={mode === 'rotate'}
        />
      </Modal.Body>
      <Modal.Actions>
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button
          variant="primary"
          disabled={!canSubmit}
          onClick={() => {
            // Trim only the name; secret VALUES are intentionally
            // sent verbatim because trailing whitespace can be
            // significant (e.g. multi-line PEMs).
            const trimmedName = name.trim()
            onSubmit(trimmedName, value)
          }}
        >
          Save
        </Button>
      </Modal.Actions>
    </Modal>
  )
}
