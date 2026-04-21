// web/src/components/RunStatusBadge.tsx
import type { RunStatus } from '../lib/types'

const styles: Record<RunStatus, string> = {
  pending:         'bg-gray-700 text-gray-300',
  running:         'bg-blue-900 text-blue-200 animate-pulse',
  succeeded:       'bg-green-900 text-green-200',
  partial_failure: 'bg-yellow-900 text-yellow-200',
  failed:          'bg-red-900 text-red-300',
}

interface Props { status: RunStatus }

export function RunStatusBadge({ status }: Props) {
  return (
    <span className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${styles[status]}`}>
      {status.replace('_', ' ')}
    </span>
  )
}
