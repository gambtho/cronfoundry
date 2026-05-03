import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { cn } from '../../lib/cn'

/**
 * Topbar — slim header strip above the page content.
 * Holds breadcrumbs on the left, search + ⌘K hint on the right.
 *
 *   <Topbar>
 *     <Topbar.Crumbs>
 *       <Topbar.Crumb href="/jobs">Jobs</Topbar.Crumb>
 *       <Topbar.Sep />
 *       <Topbar.Here>nightly-backup</Topbar.Here>
 *     </Topbar.Crumbs>
 *     <Topbar.Search />
 *   </Topbar>
 */
export function Topbar({ children }: { children: React.ReactNode }) {
  return (
    <div
      className={cn(
        'flex h-[46px] items-center gap-3.5 border-b border-rule bg-bg-2 px-6',
        'font-mono text-xs text-ink-2',
      )}
    >
      {children}
    </div>
  )
}

function Crumbs({ children }: { children: React.ReactNode }) {
  return <nav className="flex items-center gap-2">{children}</nav>
}

function Crumb({
  href,
  children,
}: {
  href: string
  children: React.ReactNode
}) {
  return (
    <a href={href} className="text-ink-2 hover:text-ink">
      {children}
    </a>
  )
}

function Sep() {
  return <span className="text-ink-4">/</span>
}

function Here({ children }: { children: React.ReactNode }) {
  return <span className="text-ink">{children}</span>
}

function Spacer() {
  return <div className="flex-1" />
}

/**
 * Search — client-side fuzzy lookup over scheduled jobs.
 *
 * Scope today is jobs only (ranked by name match → repo match →
 * skill-path match). The dropdown is a thin command-palette pattern
 * we can grow into multi-source results (runs, secrets) without the
 * call sites changing.
 *
 * Keyboard:
 *   ⌘K / Ctrl-K     focus the input from anywhere
 *   ↑ / ↓           move highlight
 *   Enter           navigate to highlighted result
 *   Escape          clear and blur (or close if just open)
 *
 * Behavior:
 *   - schedules.list() is already cached by Layout, so the query is
 *     effectively free.
 *   - Click-outside dismisses the dropdown without clearing the
 *     query, so re-focusing brings the same result back.
 *   - Empty query state shows "type to search" instead of every job
 *     in the system — operators with hundreds of schedules would
 *     find a fully-populated dropdown unhelpful.
 */
function Search({
  placeholder = 'Search jobs…',
  label = 'Search jobs',
}: {
  placeholder?: string
  /** Accessible name announced by screen readers. */
  label?: string
}) {
  const navigate = useNavigate()
  const inputRef = useRef<HTMLInputElement>(null)
  const wrapRef = useRef<HTMLDivElement>(null)

  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [highlighted, setHighlighted] = useState(0)

  const schedulesQ = useQuery({
    queryKey: ['schedules'],
    queryFn: api.schedules.list,
    // Layout already polls; we just want whatever's in cache.
    refetchOnMount: false,
    staleTime: 30_000,
  })

  // Trim once and use it everywhere visibility/result-emptiness is
  // decided. Without this, whitespace-only input would render the
  // dropdown ("No jobs match '   '") because some checks used the
  // raw query while the matcher used .trim().
  const trimmedQuery = query.trim()

  // Score each schedule against the query. Lower score = better.
  // We rank by where the match lands so name hits surface first.
  const results = useMemo(() => {
    const q = trimmedQuery.toLowerCase()
    if (q === '') return []
    const out: { id: string; name: string; subtitle: string; rank: number }[] =
      []
    for (const s of schedulesQ.data ?? []) {
      const name = s.name.toLowerCase()
      const repo = `${s.owner}/${s.repo_name}`.toLowerCase()
      const skill = (s.skill_path ?? '').toLowerCase()
      let rank = -1
      const ni = name.indexOf(q)
      const ri = repo.indexOf(q)
      const si = skill.indexOf(q)
      if (ni === 0) rank = 0
      else if (ni > 0) rank = 1
      else if (ri >= 0) rank = 2
      else if (si >= 0) rank = 3
      if (rank < 0) continue
      out.push({
        id: s.id,
        name: s.name,
        subtitle: `${s.owner}/${s.repo_name} · ${s.cron}`,
        rank,
      })
    }
    out.sort((a, b) => a.rank - b.rank || a.name.localeCompare(b.name))
    return out.slice(0, 10)
  }, [trimmedQuery, schedulesQ.data])

  // Reset highlight whenever the result list changes shape so the
  // arrow keys don't point at a removed row.
  useEffect(() => {
    setHighlighted(0)
  }, [results.length])

  // ⌘K / Ctrl-K from anywhere focuses the input.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        inputRef.current?.focus()
        inputRef.current?.select()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Click-outside closes the dropdown but keeps the query.
  useEffect(() => {
    if (!open) return
    function onPointerDown(e: PointerEvent) {
      if (
        wrapRef.current &&
        !wrapRef.current.contains(e.target as Node)
      ) {
        setOpen(false)
      }
    }
    window.addEventListener('pointerdown', onPointerDown)
    return () => window.removeEventListener('pointerdown', onPointerDown)
  }, [open])

  function commit(id: string) {
    setOpen(false)
    setQuery('')
    inputRef.current?.blur()
    navigate(`/jobs/${id}`)
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown') {
      if (results.length === 0) return
      e.preventDefault()
      setOpen(true)
      setHighlighted((h) => Math.min(results.length - 1, h + 1))
    } else if (e.key === 'ArrowUp') {
      if (results.length === 0) return
      e.preventDefault()
      setOpen(true)
      setHighlighted((h) => Math.max(0, h - 1))
    } else if (e.key === 'Enter') {
      if (results.length === 0) return
      e.preventDefault()
      const r = results[highlighted]
      if (r) commit(r.id)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      // Single, unambiguous behavior: Escape always clears the
      // search and gives focus back to the page. We don't try to
      // distinguish "first press closes, second press clears" —
      // that's surprising; users just want out.
      setOpen(false)
      setQuery('')
      inputRef.current?.blur()
    }
  }

  return (
    <div ref={wrapRef} className="relative">
      <input
        ref={inputRef}
        type="search"
        aria-label={label}
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls="topbar-search-listbox"
        autoComplete="off"
        spellCheck={false}
        className={cn(
          'w-[280px] rounded border border-rule bg-bg px-2.5 py-1.5',
          'font-mono text-xs text-ink placeholder:text-ink-3',
          'focus:border-ink-3 focus:outline-none',
        )}
        placeholder={placeholder}
        value={query}
        onChange={(e) => {
          setQuery(e.target.value)
          setOpen(true)
        }}
        onFocus={() => {
          // Only re-open if there's a real query to show results
          // for — whitespace-only counts as empty.
          if (trimmedQuery !== '') setOpen(true)
        }}
        onKeyDown={onKeyDown}
      />
      {/* keyboard hint sits OUTSIDE the wrap deliberately — keep it
          where it is in the topbar grid by placing it as a sibling
          via the parent component pattern. So we render it as part
          of this same element tree but absolutely-positioned isn't
          needed; the existing layout already places the hint after
          the input. We keep it here for portability. */}
      <span
        aria-hidden="true"
        className="ml-2 inline-block rounded border border-rule bg-bg px-1.5 text-[10px] text-ink-2"
      >
        ⌘K
      </span>

      {open && trimmedQuery !== '' && (
        <div
          id="topbar-search-listbox"
          role="listbox"
          aria-label="Job search results"
          className="absolute right-0 top-[calc(100%+6px)] z-40 w-[360px] overflow-hidden rounded border border-rule bg-bg-2 shadow-2xl"
        >
          {results.length === 0 ? (
            <div className="px-3.5 py-3 font-mono text-[11px] text-ink-3">
              No jobs match{' '}
              <span className="text-ink">"{trimmedQuery}"</span>
            </div>
          ) : (
            <ul className="m-0 list-none p-0">
              {results.map((r, i) => {
                const active = i === highlighted
                return (
                  <li key={r.id} role="option" aria-selected={active}>
                    <button
                      type="button"
                      // mouseDown not click — click loses focus to the
                      // browser before we navigate, which trips the
                      // click-outside handler.
                      onMouseDown={(e) => {
                        e.preventDefault()
                        commit(r.id)
                      }}
                      onMouseEnter={() => setHighlighted(i)}
                      className={cn(
                        'flex w-full flex-col items-start gap-0.5 px-3.5 py-2 text-left transition-colors',
                        active ? 'bg-bg-3' : 'hover:bg-bg-3',
                      )}
                    >
                      <span className="font-mono text-[12px] text-ink">
                        {r.name}
                      </span>
                      <span className="font-mono text-[10px] text-ink-3">
                        {r.subtitle}
                      </span>
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
          <div className="flex items-center gap-3 border-t border-rule bg-bg-3 px-3.5 py-1.5 font-mono text-[10px] text-ink-3">
            <span>
              <kbd className="rounded border border-rule-2 bg-bg px-1">↑</kbd>
              <kbd className="ml-1 rounded border border-rule-2 bg-bg px-1">↓</kbd>{' '}
              navigate
            </span>
            <span>
              <kbd className="rounded border border-rule-2 bg-bg px-1">↵</kbd>{' '}
              open
            </span>
            <span className="ml-auto">
              <kbd className="rounded border border-rule-2 bg-bg px-1">esc</kbd>{' '}
              close
            </span>
          </div>
        </div>
      )}
    </div>
  )
}

Topbar.Crumbs = Crumbs
Topbar.Crumb = Crumb
Topbar.Sep = Sep
Topbar.Here = Here
Topbar.Spacer = Spacer
Topbar.Search = Search
