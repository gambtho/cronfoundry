import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import type { Schedule } from '../lib/types'
import { Button, Input, Modal } from './ui'

interface Props {
  schedule: Schedule
  onClose: () => void
}

/**
 * ScheduleOverrideForm — edit cron/timezone/timeout for a single
 * schedule. YAML in the repo remains the source of truth; overrides
 * are stored separately and "Reset to YAML" wipes them.
 *
 * Backdrop-click is allowed to dismiss because the form is small,
 * keystrokes are cheap to retype, and operators frequently misclick
 * out of edit modes — letting them is kinder than locking them in.
 */
export function ScheduleOverrideForm({ schedule, onClose }: Props) {
  const qc = useQueryClient()
  const [cron, setCron] = useState(schedule.cron)
  const [timezone, setTimezone] = useState(schedule.timezone)
  // undefined when the field is empty/invalid so we can omit it from
  // the payload entirely rather than send 0/NaN.
  const [timeoutSec, setTimeoutSec] = useState<number | undefined>(
    schedule.timeout_sec,
  )

  const save = useMutation({
    mutationFn: () => {
      const overrides: {
        cron: string
        timezone: string
        timeout_sec?: number
      } = { cron, timezone }
      // Only include timeout_sec when it's a positive finite number;
      // anything else is treated as "don't touch this field".
      if (typeof timeoutSec === 'number' && Number.isFinite(timeoutSec) && timeoutSec > 0) {
        overrides.timeout_sec = timeoutSec
      }
      return api.schedules.patchOverrides(schedule.id, overrides)
    },
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
    <Modal title={`Edit schedule · ${schedule.name}`} onClose={onClose}>
      <Modal.Body>
        <p className="m-0 font-mono text-[11px] text-ink-3">
          YAML in the repo is the source of truth. Overrides apply on top.
        </p>
        <Input
          label="Cron expression"
          variant="mono"
          value={cron}
          onChange={(e) => setCron(e.target.value)}
        />
        <Input
          label="Timezone"
          variant="mono"
          value={timezone}
          onChange={(e) => setTimezone(e.target.value)}
        />
        <Input
          label="Timeout (seconds)"
          type="number"
          variant="mono"
          min={1}
          value={timeoutSec ?? ''}
          onChange={(e) => {
            // Empty string clears the override. parseInt rejects bare
            // letters; we further check finite + > 0 so 0/-1/NaN can't
            // sneak through into the payload.
            const raw = e.target.value
            if (raw === '') {
              setTimeoutSec(undefined)
              return
            }
            const n = parseInt(raw, 10)
            setTimeoutSec(Number.isFinite(n) && n > 0 ? n : undefined)
          }}
        />
      </Modal.Body>
      <Modal.Actions>
        {schedule.has_ui_overrides && (
          <Button
            variant="ghost"
            onClick={() => clear.mutate()}
            disabled={clear.isPending}
            // Push the destructive-ish "reset" affordance to the left
            // so the primary save action stays in the rightmost slot
            // where users expect it.
            className="mr-auto"
          >
            Reset to YAML
          </Button>
        )}
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        <Button
          variant="primary"
          onClick={() => save.mutate()}
          disabled={save.isPending}
        >
          Save overrides
        </Button>
      </Modal.Actions>
    </Modal>
  )
}
