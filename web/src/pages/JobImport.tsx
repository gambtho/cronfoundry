import { useState, type FormEvent } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import yaml from 'js-yaml'
import { api, type ProposeJobRequest, type ProposeJobScheduleInput } from '../lib/api'
import { isApiError } from '../lib/api-error'
import { Button, Card, PageHeader, Select, Topbar } from '../components/ui'
import { JobSuccessCard } from '../components/forms/JobSuccessCard'
import { isSafeReviewURL } from '../lib/safe-url'

const PLACEHOLDER = `name: hourly-pulse
cron: "0 * * * *"
timezone: UTC
provider: copilot-enterprise
model: gpt-5-mini
destinations:
  - github-issue:
      repo: o/r
      title: pulse`

export default function JobImport() {
  const skillsQ = useQuery({ queryKey: ['skills'], queryFn: api.skills.list })

  const [skillPath, setSkillPath] = useState('')
  const [text, setText] = useState('')
  const [parseError, setParseError] = useState<string | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [reviewURL, setReviewURL] = useState<string | null>(null)
  const [success, setSuccess] = useState<{
    pr_url: string
    pr_number: number
    branch: string
  } | null>(null)

  const propose = useMutation({
    mutationFn: api.skillRepo.proposeJob,
    onSuccess: (data) => setSuccess(data),
    onError: (err) => {
      if (isApiError(err) && err.code === 'permission_required') {
        const url = err.extras.review_url
        setReviewURL(typeof url === 'string' && isSafeReviewURL(url) ? url : null)
      }
      setSubmitError(err instanceof Error ? err.message : String(err))
    },
  })

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (propose.isPending) return // ignore double-clicks while inflight
    setParseError(null)
    setSubmitError(null)
    setReviewURL(null)
    setSuccess(null)
    if (!skillPath) {
      setSubmitError('skill is required')
      return
    }
    let parsed: unknown
    try {
      parsed = yaml.load(text)
    } catch (err) {
      setParseError(err instanceof Error ? err.message : String(err))
      return
    }
    if (!parsed || typeof parsed !== 'object') {
      setParseError('YAML must be an object')
      return
    }
    const candidate = parsed as Record<string, unknown>
    if (typeof candidate.name !== 'string' || !candidate.name) {
      setParseError('YAML must contain a `name:` field')
      return
    }
    const req: ProposeJobRequest = {
      skill_path: skillPath,
      schedule: candidate as unknown as ProposeJobScheduleInput,
    }
    propose.mutate(req)
  }

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Crumb href="/jobs">Jobs</Topbar.Crumb>
          <Topbar.Sep />
          <Topbar.Here>import</Topbar.Here>
        </Topbar.Crumbs>
      </Topbar>
      <div className="w-full max-w-[820px] px-6 pb-16 pt-7">
        <PageHeader
          title="+ Import job"
          subtitle="paste a single Schedule YAML object — opens a PR against the connected skill repo"
        />
        {success && (
          <JobSuccessCard
            prURL={success.pr_url}
            prNumber={success.pr_number}
            branch={success.branch}
          />
        )}
        <form onSubmit={onSubmit} className="grid gap-3">
          {parseError && (
            <Card>
              <p className="text-accent-red">YAML: {parseError}</p>
            </Card>
          )}
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

          <label className="grid gap-1">
            <span>Skill</span>
            <Select
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
          </label>

          <label className="grid gap-1">
            <span>Schedule YAML</span>
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={20}
              className="font-mono w-full"
              placeholder={PLACEHOLDER}
            />
          </label>

          <Button type="submit" variant="primary" disabled={propose.isPending}>
            {propose.isPending ? 'Opening PR…' : 'Open PR'}
          </Button>
        </form>
      </div>
    </>
  )
}
