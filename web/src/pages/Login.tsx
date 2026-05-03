// web/src/pages/Login.tsx

/**
 * Login — pre-auth landing. No layout chrome (no sidebar, no topbar)
 * since the user isn't authenticated yet. Pure brand mark + sign-in.
 */
export default function Login() {
  return (
    <div className="grid min-h-screen place-items-center bg-bg text-ink">
      <div className="flex w-full max-w-sm flex-col items-center gap-6 px-6">
        {/* brand */}
        <div className="flex items-center gap-2.5 font-mono text-[18px] tracking-[0.04em]">
          <span className="h-2.5 w-2.5 rounded-full bg-accent-green shadow-[0_0_12px_var(--green)]" />
          <span className="font-medium text-ink">
            cron<span className="text-accent-green">foundry</span>
          </span>
        </div>

        <p className="m-0 text-center font-mono text-[12px] text-ink-3">
          Sign in to continue.
        </p>

        <a
          href="/oauth/login"
          className="inline-flex w-full items-center justify-center gap-2 rounded border border-rule-2 bg-bg-3 px-3.5 py-2 font-mono text-[12px] text-ink transition-colors hover:border-ink-3 hover:bg-bg-4"
        >
          <GitHubMark />
          Sign in with GitHub
        </a>

        <p className="m-0 mt-2 text-center font-mono text-[10px] uppercase tracking-[0.14em] text-ink-4">
          cronfoundry · v0.4
        </p>
      </div>
    </div>
  )
}

/** Inline GitHub mark — keeps the page free of an asset dependency. */
function GitHubMark() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M8 0C3.58 0 0 3.58 0 8a8 8 0 005.47 7.59c.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  )
}
