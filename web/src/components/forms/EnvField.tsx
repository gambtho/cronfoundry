import { Input, Button } from '../ui'

export interface EnvFieldProps {
  value: Array<{ key: string; value: string }>
  onChange: (v: Array<{ key: string; value: string }>) => void
}

export function EnvField({ value, onChange }: EnvFieldProps) {
  return (
    <div className="grid gap-1">
      {value.map((kv, i) => (
        <div key={i} className="flex gap-1">
          <Input
            value={kv.key}
            onChange={(e) =>
              onChange(value.map((v, j) => (j === i ? { ...v, key: e.target.value } : v)))
            }
            placeholder="KEY"
          />
          <Input
            value={kv.value}
            onChange={(e) =>
              onChange(
                value.map((v, j) => (j === i ? { ...v, value: e.target.value } : v)),
              )
            }
            placeholder="value"
          />
          <Button onClick={() => onChange(value.filter((_, j) => j !== i))}>x</Button>
        </div>
      ))}
      <Button onClick={() => onChange([...value, { key: '', value: '' }])}>+</Button>
    </div>
  )
}

export function serializeEnv(value: Array<{ key: string; value: string }>) {
  const out: Record<string, { value: string }> = {}
  for (const { key, value: v } of value) {
    if (key) out[key] = { value: v }
  }
  return Object.keys(out).length ? out : undefined
}
