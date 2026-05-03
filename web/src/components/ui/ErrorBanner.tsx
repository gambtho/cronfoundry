import { cn } from '../../lib/cn'

/**
 * ErrorBanner — dismissible inline error strip.
 *
 * Used on settings pages (Repos / Secrets / Users) to surface
 * mutation failures that the dialog flow alone can't explain. Mirrors
 * the red-bordered failure-banner shape used on JobDetail so the
 * "something is wrong" affordance is visually consistent.
 */
interface ErrorBannerProps {
  message: string
  onDismiss?: () => void
  className?: string
}

export function ErrorBanner({ message, onDismiss, className }: ErrorBannerProps) {
  return (
    <div
      role="alert"
      className={cn(
        'mb-4 flex items-center gap-3 rounded border border-accent-red-dim bg-red-bg px-3.5 py-2.5 font-mono text-[12px] text-accent-red',
        className,
      )}
    >
      <span>{message}</span>
      {onDismiss && (
        <button
          type="button"
          onClick={onDismiss}
          className="ml-auto text-ink-3 hover:text-ink"
          aria-label="Dismiss error"
        >
          ✕
        </button>
      )}
    </div>
  )
}
