import * as React from 'react'
import { cn } from '../../lib/cn'

/**
 * Button — three variants matching the mock:
 *   - default : bordered, fills on hover
 *   - primary : green, the "do the thing" CTA (run now / new job)
 *   - ghost   : transparent, low-affordance
 *
 * Optional <kbd> hint shown on the right (e.g. "R" for run).
 */
type Variant = 'default' | 'primary' | 'ghost'

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  /** Keyboard shortcut hint shown on the right. */
  shortcut?: string
}

const VARIANT: Record<Variant, string> = {
  default:
    'border-rule-2 bg-bg-3 text-ink hover:bg-bg-4 hover:border-ink-3',
  primary:
    'border-accent-green-dim bg-accent-green-dim text-[#dffbe9] hover:bg-accent-green hover:text-[#04210f] hover:border-accent-green',
  ghost:
    'border-transparent bg-transparent text-ink-2 hover:bg-bg-3 hover:text-ink',
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant = 'default', shortcut, children, className, ...rest }, ref) => {
    return (
      <button
        ref={ref}
        className={cn(
          'inline-flex items-center justify-center gap-2 rounded border px-3.5 py-1.5',
          'font-mono text-[11.5px] transition-colors',
          'disabled:cursor-not-allowed disabled:opacity-50',
          VARIANT[variant],
          className,
        )}
        {...rest}
      >
        {children}
        {shortcut && (
          <span className="ml-auto rounded-sm border border-rule-2 px-1.5 text-[10px] text-ink-3">
            {shortcut}
          </span>
        )}
      </button>
    )
  },
)
Button.displayName = 'Button'

/**
 * IconButton — square 28×28 affordance for table-row actions
 * (▶ run, ⏸ pause). Tighter than Button.
 */
export const IconButton = React.forwardRef<
  HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement>
>(({ children, className, ...rest }, ref) => (
  <button
    ref={ref}
    className={cn(
      'inline-flex h-7 w-7 items-center justify-center rounded border border-rule-2 bg-bg-3 text-[11px] text-ink-2',
      'transition-colors hover:border-ink-3 hover:bg-bg-4 hover:text-ink',
      'disabled:cursor-not-allowed disabled:opacity-50',
      className,
    )}
    {...rest}
  >
    {children}
  </button>
))
IconButton.displayName = 'IconButton'
