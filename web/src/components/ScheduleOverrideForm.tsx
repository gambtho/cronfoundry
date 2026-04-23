import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import type { Schedule } from '../lib/types'

interface Props {
  schedule: Schedule
  onClose: () => void
}

export function ScheduleOverrideForm({ schedule, onClose }: Props) {
  const qc = useQueryClient()
  const [cron, setCron] = useState(schedule.cron)
  const [timezone, setTimezone] = useState(schedule.timezone)
  const [timeoutSec, setTimeoutSec] = useState(schedule.timeout_sec)

  const save = useMutation({
    mutationFn: () => api.schedules.patchOverrides(schedule.id, {
      cron,
      timezone,
      timeout_sec: timeoutSec,
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['schedules'] })
      onClose()
    },
  })

  const clear = useMutation({
    mutationFn: () => api.schedules.clearOverrides(schedule.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['schedules'] })
      onClose()
    },
  })

  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div className="bg-gray-900 border border-gray-700 rounded-lg p-6 w-full max-w-md space-y-4">
        <h2 className="text-lg font-semibold text-white">Edit Schedule — {schedule.name}</h2>
        <p className="text-xs text-gray-400">YAML remains the source of truth. These overrides apply on top.</p>

        <div className="space-y-3">
          <div>
            <label className="block text-xs text-gray-400 mb-1">Cron expression</label>
            <input
              className="w-full bg-gray-800 border border-gray-600 rounded px-3 py-2 text-sm text-white font-mono"
              value={cron}
              onChange={e => setCron(e.target.value)}
            />
          </div>
          <div>
            <label className="block text-xs text-gray-400 mb-1">Timezone</label>
            <input
              className="w-full bg-gray-800 border border-gray-600 rounded px-3 py-2 text-sm text-white"
              value={timezone}
              onChange={e => setTimezone(e.target.value)}
            />
          </div>
          <div>
            <label className="block text-xs text-gray-400 mb-1">Timeout (seconds)</label>
            <input
              type="number"
              className="w-full bg-gray-800 border border-gray-600 rounded px-3 py-2 text-sm text-white"
              value={timeoutSec}
              onChange={e => setTimeoutSec(Number(e.target.value))}
            />
          </div>
        </div>

        <div className="flex gap-2 pt-2">
          <button
            onClick={() => save.mutate()}
            disabled={save.isPending}
            className="flex-1 bg-indigo-700 hover:bg-indigo-600 text-white text-sm py-2 rounded disabled:opacity-50"
          >
            Save overrides
          </button>
          {schedule.has_ui_overrides && (
            <button
              onClick={() => clear.mutate()}
              disabled={clear.isPending}
              className="px-3 py-2 text-xs border border-gray-600 text-gray-300 hover:bg-gray-800 rounded disabled:opacity-50"
            >
              Reset to YAML
            </button>
          )}
          <button
            onClick={onClose}
            className="px-3 py-2 text-xs border border-gray-600 text-gray-300 hover:bg-gray-800 rounded"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}
