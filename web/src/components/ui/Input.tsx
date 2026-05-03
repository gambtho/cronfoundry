import * as React from 'react'
import { cn } from '../../lib/cn'

/**
 * Input — bordered text input matching the design tokens.
 * Wraps label + control + optional hint as a single block. The label
 * is small, uppercase, mono — same shape as Card.Header to feel
 * native to the rest of the system.
 */
type Variant = 'default' | 'mono'

interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  /** Optional label rendered above the input. Use null for unlabeled. */
  label?: React.ReactNode
  /** Optional helper text rendered below the input. */
  hint?: React.ReactNode
  /** "mono" gives the value a monospace face — good for ids, secrets. */
  variant?: Variant
}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ label, hint, variant = 'default', className, id, ...rest }, ref) => {
    // Generate a stable id when one isn't provided so we can wire
    // <label htmlFor=…> for screen readers.
    const reactId = React.useId()
    const inputId = id ?? reactId
    return (
      <div className="flex flex-col gap-1">
        {label != null && (
          <label
            htmlFor={inputId}
            className="font-mono text-[10px] uppercase tracking-[0.14em] text-ink-3"
          >
            {label}
          </label>
        )}
        <input
          ref={ref}
          id={inputId}
          className={cn(
            'rounded border border-rule bg-bg px-2.5 py-1.5 text-[13px] text-ink',
            'placeholder:text-ink-3 focus:border-ink-3 focus:outline-none',
            'disabled:cursor-not-allowed disabled:opacity-50',
            variant === 'mono' && 'font-mono',
            className,
          )}
          {...rest}
        />
        {hint != null && (
          <p className="m-0 font-mono text-[10px] text-ink-3">{hint}</p>
        )}
      </div>
    )
  },
)
Input.displayName = 'Input'

/**
 * Select — same chrome as Input but with native <select>. Native is
 * the right call here — operator tools live in keyboard, custom
 * select widgets cost more than they earn at this scale.
 */
interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {
  label?: React.ReactNode
  hint?: React.ReactNode
}

export const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  ({ label, hint, className, id, children, ...rest }, ref) => {
    const reactId = React.useId()
    const selectId = id ?? reactId
    return (
      <div className="flex flex-col gap-1">
        {label != null && (
          <label
            htmlFor={selectId}
            className="font-mono text-[10px] uppercase tracking-[0.14em] text-ink-3"
          >
            {label}
          </label>
        )}
        <select
          ref={ref}
          id={selectId}
          className={cn(
            'rounded border border-rule bg-bg px-2.5 py-1.5 text-[13px] text-ink',
            'focus:border-ink-3 focus:outline-none',
            'disabled:cursor-not-allowed disabled:opacity-50',
            className,
          )}
          {...rest}
        >
          {children}
        </select>
        {hint != null && (
          <p className="m-0 font-mono text-[10px] text-ink-3">{hint}</p>
        )}
      </div>
    )
  },
)
Select.displayName = 'Select'
