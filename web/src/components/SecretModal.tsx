// web/src/components/SecretModal.tsx
import { useState } from 'react'

interface Props {
  mode: 'create' | 'rotate'
  name?: string
  onSubmit: (name: string, value: string) => void
  onCancel: () => void
}

export function SecretModal({ mode, name: initialName = '', onSubmit, onCancel }: Props) {
  const [name, setName] = useState(initialName)
  const [value, setValue] = useState('')

  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div className="bg-gray-900 border border-gray-700 rounded-lg p-6 max-w-sm w-full mx-4">
        <h2 className="text-lg font-semibold text-white mb-4">
          {mode === 'create' ? 'Add Secret' : 'Rotate Secret'}
        </h2>
        {mode === 'create' && (
          <div className="mb-3">
            <label className="block text-sm text-gray-400 mb-1">Name</label>
            <input
              value={name}
              onChange={e => setName(e.target.value)}
              className="w-full rounded bg-gray-800 border border-gray-700 px-3 py-1.5 text-white text-sm"
              placeholder="my_api_key"
            />
          </div>
        )}
        {mode === 'rotate' && (
          <div className="mb-3 text-sm text-gray-400">
            Rotating: <span className="text-white">{initialName}</span>
          </div>
        )}
        <div className="mb-4">
          <label className="block text-sm text-gray-400 mb-1">Value</label>
          <input
            type="password"
            value={value}
            onChange={e => setValue(e.target.value)}
            className="w-full rounded bg-gray-800 border border-gray-700 px-3 py-1.5 text-white text-sm"
            placeholder="sk-..."
          />
          <p className="text-xs text-gray-600 mt-1">Value is never shown after saving.</p>
        </div>
        <div className="flex justify-end gap-3">
          <button
            onClick={onCancel}
            className="px-3 py-1.5 rounded text-sm text-gray-400 hover:text-white"
          >
            Cancel
          </button>
          <button
            onClick={() => onSubmit(name, value)}
            disabled={!name || !value}
            className="px-3 py-1.5 rounded text-sm bg-indigo-700 hover:bg-indigo-600 text-white disabled:opacity-50"
          >
            Save
          </button>
        </div>
      </div>
    </div>
  )
}
