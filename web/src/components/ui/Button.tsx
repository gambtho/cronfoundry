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

const BUTTON_BASE_CLASSES =
  'inline-flex items-center justify-center gap-2 rounded border px-3.5 py-1.5 font-mono text-[11.5px] transition-colors disabled:cursor-not-allowed disabled:opacity-50'

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant = 'default', shortcut, children, className, type, ...rest }, ref) => {
    return (
      <button
        ref={ref}
        // Default to type="button" so a Button used inside a <form>
        // doesn't accidentally submit. Callers can still override
        // (e.g. type="submit" on a real submit affordance).
        type={type ?? 'button'}
        className={cn(BUTTON_BASE_CLASSES, VARIANT[variant], className)}
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
 *
 * Icon-only buttons must carry an accessible name for screen readers,
 * so the type system forces callers to supply either `aria-label` or
 * `aria-labelledby`. A dev-only runtime guard backs this up for
 * non-TS consumers (and for the rare any-cast escape hatch).
 */
type IconButtonBaseProps = Omit<
  React.ButtonHTMLAttributes<HTMLButtonElement>,
  'aria-label' | 'aria-labelledby' | 'type'
> & {
  type?: 'button' | 'submit' | 'reset'
}
type IconButtonProps =
  | (IconButtonBaseProps & { 'aria-label': string; 'aria-labelledby'?: never })
  | (IconButtonBaseProps & { 'aria-labelledby': string; 'aria-label'?: never })

export const IconButton = React.forwardRef<HTMLButtonElement, IconButtonProps>(
  ({ children, className, type, ...rest }, ref) => {
    // Vite injects DEV at build time. We assert the shape locally so
    // we don't depend on vite/client types in the project tsconfig.
    const isDev = (import.meta as { env?: { DEV?: boolean } }).env?.DEV
    if (isDev) {
      const hasName =
        Boolean((rest as { 'aria-label'?: string })['aria-label']) ||
        Boolean((rest as { 'aria-labelledby'?: string })['aria-labelledby'])
      if (!hasName) {
        // eslint-disable-next-line no-console
        console.error(
          '[IconButton] missing aria-label or aria-labelledby — icon-only buttons need an accessible name.',
        )
      }
    }
    return (
      <button
        ref={ref}
        type={type ?? 'button'}
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
    )
  },
)
IconButton.displayName = 'IconButton'

/**
 * LinkButton — anchor styled as a Button.
 *
 * Use when the affordance navigates to a real URL (especially
 * external, target="_blank") so we don't nest a <button> inside an
 * <a>, which is invalid HTML and creates duplicate interactive
 * targets for assistive tech.
 */
interface LinkButtonProps
  extends React.AnchorHTMLAttributes<HTMLAnchorElement> {
  variant?: Variant
}

export const LinkButton = React.forwardRef<HTMLAnchorElement, LinkButtonProps>(
  ({ variant = 'default', className, children, ...rest }, ref) => (
    <a
      ref={ref}
      className={cn(BUTTON_BASE_CLASSES, VARIANT[variant], className)}
      {...rest}
    >
      {children}
    </a>
  ),
)
LinkButton.displayName = 'LinkButton'
