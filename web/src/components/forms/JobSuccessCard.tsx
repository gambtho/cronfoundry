import { Card } from '../ui'

interface Props {
  prURL: string
  prNumber: number
  branch: string
}

export function JobSuccessCard({ prURL, prNumber, branch }: Props) {
  return (
    <Card>
      <h3>PR #{prNumber} opened</h3>
      <p>
        Branch <code>{branch}</code>. Merge it on GitHub and the schedule will appear after the next sync (~60s after the merge push).
      </p>
      <a href={prURL} target="_blank" rel="noreferrer" className="underline">
        View PR →
      </a>
    </Card>
  )
}
