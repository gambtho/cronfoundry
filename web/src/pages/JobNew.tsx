import { useState, type FormEvent } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api, type ProposeJobRequest } from '../lib/api'
import { isApiError } from '../lib/api-error'
import { Button, Card, Input, PageHeader, Select, Topbar } from '../components/ui'

export default function JobNew() {
  const navigate = useNavigate()
  const skillsQ = useQuery({ queryKey: ['skills'], queryFn: api.skills.list })

  const [skillPath, setSkillPath] = useState('')
  const [name, setName] = useState('')
  const [cron, setCron] = useState('')
  const [timezone, setTimezone] = useState('UTC')
  const [provider, setProvider] = useState('copilot-enterprise')
  const [model, setModel] = useState('gpt-5-mini')

  const [submitError, setSubmitError] = useState<string | null>(null)
  const [reviewURL, setReviewURL] = useState<string | null>(null)

  const propose = useMutation({
    mutationFn: api.skillRepo.proposeJob,
    onSuccess: (data) => {
      // Task 23 replaces this with a JobSuccessCard. For v1 minimum, route
      // back to /jobs with a query string the page can render.
      navigate(`/jobs?pr=${data.pr_number}`)
    },
    onError: (err) => {
      if (isApiError(err) && err.code === 'permission_required') {
        setReviewURL((err.extras.review_url as string) ?? null)
      }
      setSubmitError(err instanceof Error ? err.message : String(err))
    },
  })

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    setSubmitError(null)
    setReviewURL(null)
    if (!skillPath || !name || !cron || !provider || !model) {
      setSubmitError('skill, name, cron, provider, and model are required')
      return
    }
    const req: ProposeJobRequest = {
      skill_path: skillPath,
      schedule: { name, cron, timezone, provider, model, destinations: [] },
    }
    propose.mutate(req)
  }

  return (
    <>
      <Topbar />
      <div className="w-full max-w-[820px] px-6 pb-16 pt-7">
        <PageHeader
          title="+ Add job"
          subtitle="propose a new schedule by opening a PR against the connected skill repo"
        />
        <form onSubmit={onSubmit} className="grid grid-cols-1 gap-3">
          {submitError && (
            <Card>
              <p className="text-accent-red">{submitError}</p>
              {reviewURL && (
                <a href={reviewURL} target="_blank" rel="noreferrer" className="underline">
                  Review the GitHub App permissions →
                </a>
              )}
            </Card>
          )}

          <Select
            label="Skill"
            value={skillPath}
            onChange={(e) => setSkillPath(e.target.value)}
            required
          >
            <option value="">— select a skill —</option>
            {(skillsQ.data ?? []).map((s) => (
              <option key={s.id} value={s.path}>
                {s.path}
              </option>
            ))}
          </Select>

          <Input
            label="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />

          <Input
            label="Cron"
            value={cron}
            onChange={(e) => setCron(e.target.value)}
            required
            placeholder="0 9 * * *"
          />

          <Input
            label="Timezone"
            value={timezone}
            onChange={(e) => setTimezone(e.target.value)}
          />

          <Input
            label="Provider"
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
            required
          />

          <Input
            label="Model"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            required
          />

          <Button type="submit" variant="primary" disabled={propose.isPending}>
            {propose.isPending ? 'Opening PR…' : 'Open PR'}
          </Button>
        </form>
      </div>
    </>
  )
}
