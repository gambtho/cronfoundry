import { Button, Input } from '../ui'

type DestType = 'github-issue' | 'slack' | 'discord' | 'teams' | 'http' | 'email'
type When = 'always' | 'on_success' | 'on_failure'

export interface DestinationsValue {
  type: DestType
  when?: When
  fields: Record<string, string>
}

export interface DestinationsFieldProps {
  value: DestinationsValue[]
  onChange: (v: DestinationsValue[]) => void
}

const FIELDS_BY_TYPE: Record<DestType, string[]> = {
  'github-issue': ['repo', 'title', 'labels'],
  slack: ['url'],
  discord: ['url'],
  teams: ['url'],
  http: ['url', 'method', 'headers'],
  email: ['to', 'subject'],
}

const DEST_TYPES = Object.keys(FIELDS_BY_TYPE) as DestType[]

export function DestinationsField({ value, onChange }: DestinationsFieldProps) {
  const update = (i: number, next: Partial<DestinationsValue>) => {
    onChange(value.map((v, idx) => (idx === i ? { ...v, ...next } : v)))
  }
  return (
    <div className="grid gap-2">
      {value.map((d, i) => (
        <div key={i} className="border p-2 rounded grid gap-1">
          <label>
            Type
            <select
              value={d.type}
              onChange={(e) =>
                update(i, { type: e.target.value as DestType, fields: {} })
              }
            >
              {DEST_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </label>
          <label>
            When
            <select
              value={d.when ?? 'always'}
              onChange={(e) => update(i, { when: e.target.value as When })}
            >
              <option value="always">always</option>
              <option value="on_success">on success</option>
              <option value="on_failure">on failure</option>
            </select>
          </label>
          {FIELDS_BY_TYPE[d.type].map((f) => (
            <label key={f} className="grid gap-1">
              <span>{f}</span>
              <Input
                value={d.fields[f] ?? ''}
                onChange={(e) =>
                  update(i, { fields: { ...d.fields, [f]: e.target.value } })
                }
              />
            </label>
          ))}
          <Button onClick={() => onChange(value.filter((_, idx) => idx !== i))}>
            remove
          </Button>
        </div>
      ))}
      <Button onClick={() => onChange([...value, { type: 'github-issue', fields: {} }])}>
        + add destination
      </Button>
    </div>
  )
}

/** Serializes the DestinationsValue array to the JSON shape expected by config.Destination[]. */
export function serializeDestinations(value: DestinationsValue[]) {
  return value.map((d) => {
    const out: Record<string, unknown> = { when: d.when ?? 'always' }
    switch (d.type) {
      case 'github-issue':
        out['github-issue'] = {
          repo: d.fields.repo,
          title: d.fields.title,
          labels: d.fields.labels
            ? d.fields.labels.split(',').map((s) => s.trim())
            : undefined,
        }
        break
      case 'slack':
      case 'discord':
      case 'teams':
        out[d.type] = { url: d.fields.url }
        break
      case 'http':
        out.http = {
          url: d.fields.url,
          method: d.fields.method || 'POST',
          headers: d.fields.headers
            ? Object.fromEntries(
                d.fields.headers
                  .split(',')
                  .map((kv) => kv.split('=').map((s) => s.trim()))
                  .filter(([k]) => !!k),
              )
            : undefined,
        }
        break
      case 'email':
        out.email = { to: d.fields.to, subject: d.fields.subject }
        break
    }
    return out
  })
}
