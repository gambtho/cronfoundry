# Pre-release Polish — Phase 3: UI Polish + Onboarding (Partial shadcn)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Adopt shadcn-style primitives for high-value components (Dialog, Sheet, DropdownMenu, Toast, DataTable, Form), and add a Dashboard onboarding empty-state that guides a fresh-install operator to their first green run.

**Architecture:** The web app already has Radix peers (`@radix-ui/react-dialog`, `@radix-ui/react-slot`, `@radix-ui/react-label`), `cva`, `clsx`, `tailwind-merge`, and `lucide-react`. We add a `web/src/components/ui/` directory holding shadcn-shaped primitives, then migrate `SecretModal`, `ConfirmDialog`, run-detail-drawer, etc., onto them. The Dashboard's empty state is replaced with a 3-step onboarding card. Run-failure surfacing on the Runs page hovers/expands to show timeline events.

**Tech Stack:** React 18, Tailwind 3, Radix primitives, vitest + React Testing Library, react-router-dom.

**Spec:** `docs/superpowers/specs/2026-04-30-prerelease-polish-design.md` §Phase 3.

**Prerequisite:** Phase 2 dogfood is complete (so screenshots taken at the end represent the deployed reality).

---

## File Structure

**Create:**
- `web/src/components/ui/dialog.tsx` — shadcn Dialog (Radix primitives + Tailwind tokens)
- `web/src/components/ui/alert-dialog.tsx` — destructive-confirm pattern
- `web/src/components/ui/sheet.tsx` — side-drawer for run details
- `web/src/components/ui/dropdown-menu.tsx` — row actions, user menu
- `web/src/components/ui/toast.tsx` + `web/src/components/ui/toaster.tsx` + `web/src/lib/use-toast.ts` — toast system
- `web/src/components/ui/data-table.tsx` — generic sortable/filterable table (tanstack-table light wrapper)
- `web/src/components/ui/form.tsx` — react-hook-form + zod wrappers
- `web/src/components/ui/button.tsx`, `input.tsx`, `label.tsx`, `badge.tsx`, `card.tsx` — base primitives needed by composites
- `web/src/lib/cn.ts` — `cn(...)` utility (`clsx` + `tailwind-merge`)
- `web/src/components/Onboarding.tsx` — Dashboard empty-state component
- `web/src/components/Onboarding.test.tsx`
- `web/src/components/RunDetailSheet.tsx` — promoted from inline drawer
- `web/src/components/RunDetailSheet.test.tsx`

**Modify:**
- `web/tailwind.config.ts` — add shadcn CSS-variable theme tokens
- `web/src/index.css` — add CSS custom properties for tokens
- `web/src/components/SecretModal.tsx` — use `Dialog`
- `web/src/components/ConfirmDialog.tsx` — use `AlertDialog`
- `web/src/components/RunStatusBadge.tsx` — use `Badge` base
- `web/src/components/Layout.tsx` — adopt new tokens, add user `DropdownMenu`
- `web/src/pages/Dashboard.tsx` — render `Onboarding` when zero schedules
- `web/src/pages/Runs.tsx` — use `DataTable` and `RunDetailSheet`; surface failure first-line on hover
- `web/src/pages/Audit.tsx` — use `DataTable`
- `web/src/pages/Repos.tsx`, `Secrets.tsx`, `Providers.tsx`, `Users.tsx` — use shadcn `Form` + `Dialog`

**Add deps:**
- `@radix-ui/react-dropdown-menu`, `@radix-ui/react-toast`, `@radix-ui/react-alert-dialog`, `@radix-ui/react-tabs` (transitive) — runtime
- `@tanstack/react-table` — for DataTable
- `react-hook-form`, `zod`, `@hookform/resolvers` — for Form

---

## Task 1: Add `cn` utility and shadcn theme tokens

**Files:**
- Create: `web/src/lib/cn.ts`
- Create: `web/src/lib/cn.test.ts`
- Modify: `web/tailwind.config.ts`
- Modify: `web/src/index.css`

- [ ] **Step 1: Failing test for `cn`**

```ts
// web/src/lib/cn.test.ts
import { describe, it, expect } from 'vitest'
import { cn } from './cn'

describe('cn', () => {
  it('joins class names', () => {
    expect(cn('a', 'b')).toBe('a b')
  })
  it('drops falsy', () => {
    expect(cn('a', false, null, undefined, 'b')).toBe('a b')
  })
  it('merges tailwind utilities (later wins)', () => {
    expect(cn('p-2', 'p-4')).toBe('p-4')
  })
})
```

- [ ] **Step 2: Verify fails**

Run: `cd web && npx vitest run src/lib/cn.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

```ts
// web/src/lib/cn.ts
import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}
```

- [ ] **Step 4: Tests pass**

Run: `cd web && npx vitest run src/lib/cn.test.ts`
Expected: PASS.

- [ ] **Step 5: Add CSS-variable theme tokens to index.css**

Replace `web/src/index.css` body with:

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  :root {
    --background: 222 47% 6%;
    --foreground: 210 40% 96%;
    --card: 222 47% 8%;
    --card-foreground: 210 40% 96%;
    --popover: 222 47% 8%;
    --popover-foreground: 210 40% 96%;
    --primary: 24 95% 53%;        /* foundry orange */
    --primary-foreground: 222 47% 6%;
    --secondary: 217 33% 17%;
    --secondary-foreground: 210 40% 96%;
    --muted: 217 33% 14%;
    --muted-foreground: 215 20% 65%;
    --accent: 217 33% 17%;
    --accent-foreground: 210 40% 96%;
    --destructive: 0 72% 51%;
    --destructive-foreground: 210 40% 96%;
    --success: 142 71% 45%;
    --success-foreground: 0 0% 100%;
    --warning: 38 92% 50%;
    --warning-foreground: 222 47% 6%;
    --border: 217 33% 17%;
    --input: 217 33% 17%;
    --ring: 24 95% 53%;
    --radius: 0.5rem;
  }

  * { @apply border-border; }
  body {
    @apply bg-background text-foreground;
    font-feature-settings: "rlig" 1, "calt" 1;
  }
}
```

- [ ] **Step 6: Update tailwind.config.ts to use the tokens**

```ts
// web/tailwind.config.ts (replace `theme.extend.colors` block)
import type { Config } from 'tailwindcss'

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    container: { center: true, padding: '2rem', screens: { '2xl': '1400px' } },
    extend: {
      colors: {
        border:      'hsl(var(--border))',
        input:       'hsl(var(--input))',
        ring:        'hsl(var(--ring))',
        background:  'hsl(var(--background))',
        foreground:  'hsl(var(--foreground))',
        primary: {
          DEFAULT:    'hsl(var(--primary))',
          foreground: 'hsl(var(--primary-foreground))',
        },
        secondary: {
          DEFAULT:    'hsl(var(--secondary))',
          foreground: 'hsl(var(--secondary-foreground))',
        },
        destructive: {
          DEFAULT:    'hsl(var(--destructive))',
          foreground: 'hsl(var(--destructive-foreground))',
        },
        success: {
          DEFAULT:    'hsl(var(--success))',
          foreground: 'hsl(var(--success-foreground))',
        },
        warning: {
          DEFAULT:    'hsl(var(--warning))',
          foreground: 'hsl(var(--warning-foreground))',
        },
        muted: {
          DEFAULT:    'hsl(var(--muted))',
          foreground: 'hsl(var(--muted-foreground))',
        },
        accent: {
          DEFAULT:    'hsl(var(--accent))',
          foreground: 'hsl(var(--accent-foreground))',
        },
        popover: {
          DEFAULT:    'hsl(var(--popover))',
          foreground: 'hsl(var(--popover-foreground))',
        },
        card: {
          DEFAULT:    'hsl(var(--card))',
          foreground: 'hsl(var(--card-foreground))',
        },
      },
      borderRadius: {
        lg: 'var(--radius)',
        md: 'calc(var(--radius) - 2px)',
        sm: 'calc(var(--radius) - 4px)',
      },
    },
  },
  plugins: [],
} satisfies Config
```

- [ ] **Step 7: Build the UI to verify no regression**

```bash
cd web && npm run build
```

Expected: builds successfully. Existing pages may look slightly different (background color changed); that's expected — Track A's goal.

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/cn.ts web/src/lib/cn.test.ts web/src/index.css web/tailwind.config.ts
git commit -m "feat(ui): add cn utility and shadcn-shaped CSS-variable theme tokens"
```

---

## Task 2: Add base primitives — Button, Input, Label, Badge, Card

**Files:**
- Create: `web/src/components/ui/button.tsx`, `input.tsx`, `label.tsx`, `badge.tsx`, `card.tsx`
- Create: tests for each.

These are the building blocks. Each is one file, ~30-50 LOC, drop-in copy from shadcn's reference (https://ui.shadcn.com/docs/components — but we're not running their CLI, just using their patterns).

- [ ] **Step 1: Failing test for Button**

```tsx
// web/src/components/ui/button.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Button } from './button'

describe('Button', () => {
  it('renders children', () => {
    render(<Button>Click me</Button>)
    expect(screen.getByRole('button', { name: 'Click me' })).toBeInTheDocument()
  })
  it('applies destructive variant', () => {
    render(<Button variant="destructive">Delete</Button>)
    const btn = screen.getByRole('button')
    expect(btn.className).toMatch(/destructive/)
  })
  it('renders as a Slot when asChild', () => {
    render(<Button asChild><a href="/x">Link</a></Button>)
    expect(screen.getByRole('link', { name: 'Link' })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Tests fail**

Run: `cd web && npx vitest run src/components/ui/button.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement Button**

```tsx
// web/src/components/ui/button.tsx
import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/cn'

const buttonVariants = cva(
  'inline-flex items-center justify-center whitespace-nowrap rounded-md text-sm font-medium ' +
  'ring-offset-background transition-colors ' +
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 ' +
  'disabled:pointer-events-none disabled:opacity-50',
  {
    variants: {
      variant: {
        default:     'bg-primary text-primary-foreground hover:bg-primary/90',
        destructive: 'bg-destructive text-destructive-foreground hover:bg-destructive/90',
        outline:     'border border-input bg-background hover:bg-accent hover:text-accent-foreground',
        secondary:   'bg-secondary text-secondary-foreground hover:bg-secondary/80',
        ghost:       'hover:bg-accent hover:text-accent-foreground',
        link:        'text-primary underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-10 px-4 py-2',
        sm: 'h-9 rounded-md px-3',
        lg: 'h-11 rounded-md px-8',
        icon: 'h-10 w-10',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return <Comp className={cn(buttonVariants({ variant, size, className }))} ref={ref} {...props} />
  },
)
Button.displayName = 'Button'

export { buttonVariants }
```

Note: `@/lib/cn` requires path alias. If not configured, use relative `../../lib/cn`. Check `vite.config.ts` and `tsconfig.json` for `@/*` mapping.

- [ ] **Step 4: Add path alias if missing**

Modify `web/vite.config.ts`:

```ts
import path from 'node:path'
// ...
export default defineConfig({
  plugins: [react()],
  resolve: { alias: { '@': path.resolve(__dirname, 'src') } },
  // ...
})
```

Modify `web/tsconfig.json` `compilerOptions`:

```json
{
  "baseUrl": ".",
  "paths": { "@/*": ["src/*"] }
}
```

- [ ] **Step 5: Tests pass**

Run: `cd web && npx vitest run src/components/ui/button.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 6: Repeat steps 1-5 for Input, Label, Badge, Card**

Implement each as a thin wrapper. Reference: https://ui.shadcn.com/docs/components/{input,label,badge,card}. Each gets a small test that asserts (a) it renders, (b) variant prop applies the expected class.

```tsx
// web/src/components/ui/input.tsx
import * as React from 'react'
import { cn } from '@/lib/cn'

export type InputProps = React.InputHTMLAttributes<HTMLInputElement>

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, type, ...props }, ref) => (
    <input
      type={type}
      className={cn(
        'flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm',
        'ring-offset-background file:border-0 file:bg-transparent file:text-sm file:font-medium',
        'placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2',
        'focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      ref={ref}
      {...props}
    />
  ),
)
Input.displayName = 'Input'
```

```tsx
// web/src/components/ui/label.tsx
import * as React from 'react'
import * as LabelPrimitive from '@radix-ui/react-label'
import { cn } from '@/lib/cn'

export const Label = React.forwardRef<
  React.ElementRef<typeof LabelPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof LabelPrimitive.Root>
>(({ className, ...props }, ref) => (
  <LabelPrimitive.Root
    ref={ref}
    className={cn('text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70', className)}
    {...props}
  />
))
Label.displayName = LabelPrimitive.Root.displayName
```

```tsx
// web/src/components/ui/badge.tsx
import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/cn'

const badgeVariants = cva(
  'inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors',
  {
    variants: {
      variant: {
        default:     'border-transparent bg-primary text-primary-foreground',
        secondary:   'border-transparent bg-secondary text-secondary-foreground',
        destructive: 'border-transparent bg-destructive text-destructive-foreground',
        success:     'border-transparent bg-success text-success-foreground',
        warning:     'border-transparent bg-warning text-warning-foreground',
        outline:     'text-foreground',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />
}
```

```tsx
// web/src/components/ui/card.tsx
import * as React from 'react'
import { cn } from '@/lib/cn'

export const Card = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn('rounded-lg border bg-card text-card-foreground shadow-sm', className)} {...props} />
  ),
)
Card.displayName = 'Card'

export const CardHeader = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => <div ref={ref} className={cn('flex flex-col space-y-1.5 p-6', className)} {...props} />,
)
CardHeader.displayName = 'CardHeader'

export const CardTitle = React.forwardRef<HTMLHeadingElement, React.HTMLAttributes<HTMLHeadingElement>>(
  ({ className, ...props }, ref) => <h3 ref={ref} className={cn('text-lg font-semibold leading-none tracking-tight', className)} {...props} />,
)
CardTitle.displayName = 'CardTitle'

export const CardDescription = React.forwardRef<HTMLParagraphElement, React.HTMLAttributes<HTMLParagraphElement>>(
  ({ className, ...props }, ref) => <p ref={ref} className={cn('text-sm text-muted-foreground', className)} {...props} />,
)
CardDescription.displayName = 'CardDescription'

export const CardContent = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => <div ref={ref} className={cn('p-6 pt-0', className)} {...props} />,
)
CardContent.displayName = 'CardContent'

export const CardFooter = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => <div ref={ref} className={cn('flex items-center p-6 pt-0', className)} {...props} />,
)
CardFooter.displayName = 'CardFooter'
```

Each gets a smoke test (renders without crashing, applies the expected class for one variant).

- [ ] **Step 7: All tests pass**

Run: `cd web && npx vitest run src/components/ui/`
Expected: All passing (Button: 3, Input: 1+, Label: 1+, Badge: 2+, Card: 1+).

- [ ] **Step 8: Commit**

```bash
git add web/src/components/ui/ web/vite.config.ts web/tsconfig.json
git commit -m "feat(ui): add shadcn base primitives (button, input, label, badge, card)"
```

---

## Task 3: Migrate `RunStatusBadge` to use the new `Badge` primitive

**Files:**
- Modify: `web/src/components/RunStatusBadge.tsx`
- Test: existing (verify no regression).

- [ ] **Step 1: Read the current implementation**

```bash
cat web/src/components/RunStatusBadge.tsx
```

- [ ] **Step 2: Rewrite using `Badge`**

```tsx
// web/src/components/RunStatusBadge.tsx
import { Badge } from './ui/badge'

type RunStatus = 'pending' | 'running' | 'succeeded' | 'partial_failure' | 'failed'

const variantFor: Record<RunStatus, 'default' | 'success' | 'warning' | 'destructive' | 'secondary'> = {
  pending:         'secondary',
  running:         'default',
  succeeded:       'success',
  partial_failure: 'warning',
  failed:          'destructive',
}

const labelFor: Record<RunStatus, string> = {
  pending:         'Pending',
  running:         'Running',
  succeeded:       'Succeeded',
  partial_failure: 'Partial',
  failed:          'Failed',
}

export function RunStatusBadge({ status }: { status: RunStatus }) {
  return <Badge variant={variantFor[status]}>{labelFor[status]}</Badge>
}
```

- [ ] **Step 3: Add tests**

```tsx
// web/src/components/RunStatusBadge.test.tsx (new)
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { RunStatusBadge } from './RunStatusBadge'

describe('RunStatusBadge', () => {
  it('renders succeeded as success variant', () => {
    render(<RunStatusBadge status="succeeded" />)
    expect(screen.getByText('Succeeded')).toBeInTheDocument()
  })
  it('renders failed as destructive', () => {
    render(<RunStatusBadge status="failed" />)
    const el = screen.getByText('Failed')
    expect(el.className).toMatch(/destructive/)
  })
})
```

- [ ] **Step 4: Run tests**

Run: `cd web && npx vitest run src/components/RunStatusBadge.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/RunStatusBadge.tsx web/src/components/RunStatusBadge.test.tsx
git commit -m "refactor(ui): migrate RunStatusBadge to Badge primitive"
```

---

## Task 4: Add Dialog primitive + migrate `SecretModal`

**Files:**
- Create: `web/src/components/ui/dialog.tsx`
- Modify: `web/src/components/SecretModal.tsx`
- Test: vitest tests.

- [ ] **Step 1: Implement Dialog primitive**

(Standard shadcn Dialog wrapping `@radix-ui/react-dialog` — copy the reference pattern.)

```tsx
// web/src/components/ui/dialog.tsx
import * as React from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { cn } from '@/lib/cn'

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogPortal = DialogPrimitive.Portal
export const DialogClose = DialogPrimitive.Close

export const DialogOverlay = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      'fixed inset-0 z-50 bg-black/80 data-[state=open]:animate-in data-[state=closed]:animate-out',
      'data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
      className,
    )}
    {...props}
  />
))
DialogOverlay.displayName = DialogPrimitive.Overlay.displayName

export const DialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>
>(({ className, children, ...props }, ref) => (
  <DialogPortal>
    <DialogOverlay />
    <DialogPrimitive.Content
      ref={ref}
      className={cn(
        'fixed left-[50%] top-[50%] z-50 grid w-full max-w-lg translate-x-[-50%] translate-y-[-50%] gap-4',
        'border bg-background p-6 shadow-lg duration-200 sm:rounded-lg',
        className,
      )}
      {...props}
    >
      {children}
      <DialogPrimitive.Close className="absolute right-4 top-4 rounded-sm opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:pointer-events-none">
        <X className="h-4 w-4" />
        <span className="sr-only">Close</span>
      </DialogPrimitive.Close>
    </DialogPrimitive.Content>
  </DialogPortal>
))
DialogContent.displayName = DialogPrimitive.Content.displayName

export const DialogHeader = ({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn('flex flex-col space-y-1.5 text-center sm:text-left', className)} {...props} />
)
DialogHeader.displayName = 'DialogHeader'

export const DialogFooter = ({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn('flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2', className)} {...props} />
)
DialogFooter.displayName = 'DialogFooter'

export const DialogTitle = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Title ref={ref} className={cn('text-lg font-semibold leading-none tracking-tight', className)} {...props} />
))
DialogTitle.displayName = DialogPrimitive.Title.displayName

export const DialogDescription = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Description ref={ref} className={cn('text-sm text-muted-foreground', className)} {...props} />
))
DialogDescription.displayName = DialogPrimitive.Description.displayName
```

- [ ] **Step 2: Read current SecretModal**

```bash
cat web/src/components/SecretModal.tsx
```

- [ ] **Step 3: Add a failing test that the migrated SecretModal still works**

```tsx
// web/src/components/SecretModal.test.tsx (new)
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SecretModal } from './SecretModal'

describe('SecretModal', () => {
  it('calls onSubmit with the entered name and value', () => {
    const onSubmit = vi.fn()
    render(<SecretModal open={true} onOpenChange={() => {}} onSubmit={onSubmit} />)
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'slack_webhook' } })
    fireEvent.change(screen.getByLabelText(/value/i), { target: { value: 'https://x' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    expect(onSubmit).toHaveBeenCalledWith({ name: 'slack_webhook', value: 'https://x' })
  })
  it('does not render content when open is false', () => {
    render(<SecretModal open={false} onOpenChange={() => {}} onSubmit={() => {}} />)
    expect(screen.queryByText(/secret/i)).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 4: Tests fail or pass against current impl**

Run: `cd web && npx vitest run src/components/SecretModal.test.tsx`

If current `SecretModal` has a different API (e.g. `isOpen` not `open`), record the actual API and update the test to match what we want post-migration.

- [ ] **Step 5: Migrate SecretModal**

```tsx
// web/src/components/SecretModal.tsx
import { useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from './ui/dialog'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Label } from './ui/label'

interface SecretModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (data: { name: string; value: string }) => void
}

export function SecretModal({ open, onOpenChange, onSubmit }: SecretModalProps) {
  const [name, setName] = useState('')
  const [value, setValue] = useState('')

  const handleSave = () => {
    onSubmit({ name, value })
    setName('')
    setValue('')
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add a secret</DialogTitle>
          <DialogDescription>Stored encrypted at rest with the master key.</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="secret-name">Name</Label>
            <Input id="secret-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="slack_webhook" />
          </div>
          <div className="space-y-2">
            <Label htmlFor="secret-value">Value</Label>
            <Input id="secret-value" type="password" value={value} onChange={(e) => setValue(e.target.value)} />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button onClick={handleSave} disabled={!name || !value}>Save</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// Default export kept for backward compatibility with current imports
export default SecretModal
```

- [ ] **Step 6: Update Secrets.tsx if its consumer signature changed**

Find consumers: `grep -rn "SecretModal" web/src/`
Update prop names if needed (`isOpen`→`open`, `onClose`→`onOpenChange`, etc.).

- [ ] **Step 7: Tests pass**

Run: `cd web && npx vitest run`
Expected: all green.

- [ ] **Step 8: Manual smoke**

```bash
cd web && npm run dev
```

Visit `/secrets`, click "Add", verify the dialog opens and submits.

- [ ] **Step 9: Commit**

```bash
git add web/src/components/ui/dialog.tsx web/src/components/SecretModal.tsx web/src/components/SecretModal.test.tsx web/src/pages/Secrets.tsx
git commit -m "feat(ui): Dialog primitive; migrate SecretModal"
```

---

## Task 5: Add AlertDialog primitive + migrate `ConfirmDialog`

Same pattern as Task 4. Reference: https://ui.shadcn.com/docs/components/alert-dialog. Wraps `@radix-ui/react-alert-dialog`.

- [ ] **Step 1: `npm install @radix-ui/react-alert-dialog --workspace web`**
- [ ] **Step 2: Create `web/src/components/ui/alert-dialog.tsx`** (copy shadcn reference, swap classes for our `cn` and tokens)
- [ ] **Step 3: Migrate `ConfirmDialog.tsx`** to use `AlertDialog`
- [ ] **Step 4: Add a test that confirms the destructive action only fires after explicit confirm**
- [ ] **Step 5: Update consumers**: `grep -rn "ConfirmDialog" web/src/` and update.
- [ ] **Step 6: Run all tests, commit**

```bash
git commit -m "feat(ui): AlertDialog primitive; migrate ConfirmDialog"
```

---

## Task 6: Add Sheet primitive + extract `RunDetailSheet`

Run details currently render inline on the Runs page (or in an ad-hoc div drawer). Promote to a proper `Sheet`. The `LogTail` component lives inside.

**Files:**
- Create: `web/src/components/ui/sheet.tsx` (Radix Dialog with side animation)
- Create: `web/src/components/RunDetailSheet.tsx`
- Create: `web/src/components/RunDetailSheet.test.tsx`
- Modify: `web/src/pages/Runs.tsx`

- [ ] **Step 1: Read current run-detail rendering** in `Runs.tsx`. Identify what props it needs (run ID, run object, close handler).

- [ ] **Step 2: Implement `Sheet` primitive** — copy from shadcn reference; binds Radix Dialog primitives but with a side variant (`right`, `left`, `top`, `bottom`).

- [ ] **Step 3: Extract `RunDetailSheet`** — receives `{ runId: string | null; onClose: () => void }`, renders the Sheet, fetches run via react-query, includes `LogTail` for in-flight runs.

- [ ] **Step 4: Failing test**

```tsx
// web/src/components/RunDetailSheet.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RunDetailSheet } from './RunDetailSheet'

function withQuery(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>
}

describe('RunDetailSheet', () => {
  it('does not render when runId is null', () => {
    render(withQuery(<RunDetailSheet runId={null} onClose={() => {}} />))
    expect(screen.queryByText(/run /i)).not.toBeInTheDocument()
  })

  it('renders run details when runId is set', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ id: 'r-1', status: 'succeeded', schedule_name: 'every-5', llm_provider: 'copilot-enterprise' }),
    } as Response)
    render(withQuery(<RunDetailSheet runId="r-1" onClose={() => {}} />))
    await waitFor(() => {
      expect(screen.getByText(/every-5/)).toBeInTheDocument()
    })
  })
})
```

- [ ] **Step 5: Implement `RunDetailSheet`** to make the test pass.

- [ ] **Step 6: Update `Runs.tsx`** — manage `selectedRunId` in state, render `<RunDetailSheet />`, click row to set ID.

- [ ] **Step 7: Run tests, commit**

```bash
git commit -m "feat(ui): Sheet primitive; extract RunDetailSheet from Runs page"
```

---

## Task 7: Add DropdownMenu primitive + adopt in Layout user menu and row actions

**Files:**
- Create: `web/src/components/ui/dropdown-menu.tsx`
- Modify: `web/src/components/Layout.tsx` to add a user menu (sign-out moved into it)
- Modify: `web/src/pages/Schedules.tsx` (or similar) to add row actions (Run now, Pause, Edit) — these placeholders for Phase 5b.

- [ ] **Step 1: `npm install @radix-ui/react-dropdown-menu --workspace web`**
- [ ] **Step 2: Implement `dropdown-menu.tsx`** (copy from shadcn reference)
- [ ] **Step 3: Update `Layout.tsx`** — replace the bottom "Sign out" link with a user menu showing the current user's login + sign-out item.
- [ ] **Step 4: Test for Layout user menu**

```tsx
// web/src/components/Layout.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import Layout from './Layout'

describe('Layout', () => {
  it('shows user menu trigger', () => {
    render(<MemoryRouter><Layout /></MemoryRouter>)
    expect(screen.getByRole('button', { name: /menu|account/i })).toBeInTheDocument()
  })
})
```

- [ ] **Step 5: Run tests, commit**

```bash
git commit -m "feat(ui): DropdownMenu primitive; user menu in Layout"
```

---

## Task 8: Add Toast system + replace `alert()` calls

**Files:**
- Create: `web/src/components/ui/toast.tsx`, `toaster.tsx`
- Create: `web/src/lib/use-toast.ts`
- Modify: `web/src/main.tsx` to render `<Toaster />`
- Modify: every `alert(...)` or inline error banner in pages to call `toast()`

- [ ] **Step 1: `npm install @radix-ui/react-toast --workspace web`**
- [ ] **Step 2: Implement toast system** — copy shadcn reference for `toast.tsx`, `toaster.tsx`, `use-toast.ts`.
- [ ] **Step 3: Render `<Toaster />`** at root in `main.tsx`.
- [ ] **Step 4: Replace alerts** — `grep -rn "alert(" web/src/` and replace each.
- [ ] **Step 5: Test**

```tsx
// web/src/lib/use-toast.test.ts (smoke)
import { describe, it, expect, act } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useToast } from './use-toast'

describe('useToast', () => {
  it('exposes toast function and toasts array', () => {
    const { result } = renderHook(() => useToast())
    expect(typeof result.current.toast).toBe('function')
    expect(Array.isArray(result.current.toasts)).toBe(true)
  })
})
```

- [ ] **Step 6: Commit**

```bash
git commit -m "feat(ui): Toast system; replace alert() calls"
```

---

## Task 9: Add `Onboarding` component + render on empty Dashboard

**Files:**
- Create: `web/src/components/Onboarding.tsx`
- Create: `web/src/components/Onboarding.test.tsx`
- Modify: `web/src/pages/Dashboard.tsx`

The Dashboard, when there are zero schedules, shows a 3-step card:

1. ✅ **Provider connected** — green check + "Connected: Copilot Enterprise" / red X + "Connect Provider" link.
2. ✅ **Skill repo connected** — same pattern.
3. ⏳ **Waiting for first run** — countdown to next fire OR "First run completed at HH:MM" once one lands.

- [ ] **Step 1: Failing test**

```tsx
// web/src/components/Onboarding.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { Onboarding } from './Onboarding'

describe('Onboarding', () => {
  it('shows three steps', () => {
    render(<MemoryRouter><Onboarding providersConnected={false} reposConnected={false} firstRunAt={null} /></MemoryRouter>)
    expect(screen.getByText(/connect provider/i)).toBeInTheDocument()
    expect(screen.getByText(/connect repo/i)).toBeInTheDocument()
    expect(screen.getByText(/first run/i)).toBeInTheDocument()
  })
  it('shows green checks when steps are done', () => {
    render(<MemoryRouter><Onboarding providersConnected={true} reposConnected={true} firstRunAt={null} /></MemoryRouter>)
    expect(screen.queryByText(/connect provider/i)).not.toBeInTheDocument()
    expect(screen.getByText(/provider connected/i)).toBeInTheDocument()
  })
  it('shows first-run-completed when firstRunAt is set', () => {
    render(<MemoryRouter><Onboarding providersConnected={true} reposConnected={true} firstRunAt="2026-04-30T12:00:00Z" /></MemoryRouter>)
    expect(screen.getByText(/first run completed/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Implement**

```tsx
// web/src/components/Onboarding.tsx
import { Link } from 'react-router-dom'
import { Check, X, Clock } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from './ui/card'
import { cn } from '@/lib/cn'

interface Props {
  providersConnected: boolean
  reposConnected: boolean
  firstRunAt: string | null
}

function Step({ done, label, action }: { done: boolean; label: string; action?: React.ReactNode }) {
  return (
    <div className="flex items-center gap-3 py-3">
      <div className={cn(
        'flex h-8 w-8 items-center justify-center rounded-full',
        done ? 'bg-success text-success-foreground' : 'bg-muted text-muted-foreground',
      )}>
        {done ? <Check className="h-4 w-4" /> : <X className="h-4 w-4" />}
      </div>
      <div className="flex-1">
        <div className="text-sm font-medium">{label}</div>
        {!done && action && <div className="mt-1">{action}</div>}
      </div>
    </div>
  )
}

export function Onboarding({ providersConnected, reposConnected, firstRunAt }: Props) {
  return (
    <Card className="max-w-2xl">
      <CardHeader>
        <CardTitle>Get started</CardTitle>
      </CardHeader>
      <CardContent>
        <Step
          done={providersConnected}
          label={providersConnected ? 'Provider connected' : 'Connect provider'}
          action={<Link to="/providers" className="text-sm text-primary hover:underline">Connect a provider →</Link>}
        />
        <Step
          done={reposConnected}
          label={reposConnected ? 'Skill repo connected' : 'Connect repo'}
          action={<Link to="/repos" className="text-sm text-primary hover:underline">Connect a repo →</Link>}
        />
        <div className="flex items-center gap-3 py-3">
          <div className={cn(
            'flex h-8 w-8 items-center justify-center rounded-full',
            firstRunAt ? 'bg-success text-success-foreground' : 'bg-muted text-muted-foreground',
          )}>
            {firstRunAt ? <Check className="h-4 w-4" /> : <Clock className="h-4 w-4 animate-pulse" />}
          </div>
          <div className="flex-1">
            <div className="text-sm font-medium">
              {firstRunAt
                ? `First run completed at ${new Date(firstRunAt).toLocaleTimeString()}`
                : 'Waiting for first run'}
            </div>
            {!firstRunAt && (
              <div className="mt-1 text-xs text-muted-foreground">
                Push a <code>cronfoundry.yaml</code> with a schedule to your connected repo.
              </div>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
```

- [ ] **Step 3: Render in Dashboard when empty**

In `Dashboard.tsx`, fetch the schedule list, providers list, and recent runs. If schedules.length === 0, render `<Onboarding ... />` with the appropriate flags. If schedules > 0, hide.

- [ ] **Step 4: Tests pass, commit**

```bash
git commit -m "feat(ui): Onboarding empty-state on Dashboard"
```

---

## Task 10: Surface failure first-line on Runs row hover, expand on click

**Files:**
- Modify: `web/src/pages/Runs.tsx` (or wherever the Runs list table lives)
- Test: vitest

- [ ] **Step 1: Add `failure_summary` to the run schema** (if not already in the API response). Check `internal/webapi/runs.go` and `web/src/lib/types.ts`. If missing, add a Go-side derivation: first line of the first non-success timeline event.

- [ ] **Step 2: Tooltip on the status badge**

If the run is `failed` or `partial_failure`, wrap the badge in a Radix Tooltip showing `run.failure_summary`. Add `Tooltip` primitive at `web/src/components/ui/tooltip.tsx` (similar pattern to others).

- [ ] **Step 3: Click row → opens RunDetailSheet** (already in Task 6).

- [ ] **Step 4: Test**

```tsx
// integration-style test
it('shows failure summary on hover for failed run', async () => {
  // render Runs with a stubbed react-query for a failed run
  // hover the status cell, assert tooltip content
})
```

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(ui): surface failure summary on Runs row hover"
```

---

## Task 11: Migrate Audit and Runs to DataTable; add Form helpers

**Files:**
- Create: `web/src/components/ui/data-table.tsx`
- Create: `web/src/components/ui/form.tsx` (react-hook-form + zod wrappers)
- Modify: `Runs.tsx`, `Audit.tsx`, `Repos.tsx`, `Secrets.tsx`, `Providers.tsx`, `Users.tsx`

- [ ] **Step 1: `npm install @tanstack/react-table react-hook-form zod @hookform/resolvers --workspace web`**
- [ ] **Step 2: Implement `data-table.tsx`** — generic over a column definition, sortable, with optional filtering. Reference: https://ui.shadcn.com/docs/components/data-table.
- [ ] **Step 3: Migrate Runs.tsx** to use DataTable; columns: status, schedule, started, duration, tokens (placeholder for Phase 5c).
- [ ] **Step 4: Migrate Audit.tsx** to use DataTable; columns: time, actor, action, target.
- [ ] **Step 5: Implement `form.tsx`** — Form, FormItem, FormLabel, FormControl, FormDescription, FormMessage, FormField. Reference: https://ui.shadcn.com/docs/components/form.
- [ ] **Step 6: Migrate Repos.tsx connect-repo form, Secrets.tsx add-secret form, Providers.tsx connect form, Users.tsx add-user form** to use the Form helpers + zod schemas. Each gets a small validation-test.
- [ ] **Step 7: Run all tests, build, commit**

```bash
git commit -m "feat(ui): DataTable + Form primitives; migrate list pages"
```

---

## Task 12: Visual smoke pass + screenshot prep

- [ ] **Step 1: `cd web && npm run build`** — confirm clean build.
- [ ] **Step 2: `cd web && npm run dev`** — open every page, eyeball each. Note any visual issues in `docs/superpowers/specs/<date>-ui-polish-punch.md`.
- [ ] **Step 3: Fix anything in the punch list** — small commits per fix.
- [ ] **Step 4: Take screenshots** for Phase 4:
  - Dashboard with the onboarding card (zero schedules)
  - Dashboard with a recent run + tokens tile
  - Runs page with green + partial_failure entries
  - Run detail Sheet open with live LogTail
  Save to `docs/assets/` (create if missing).

- [ ] **Step 5: Commit**

```bash
git add docs/assets/
git commit -m "docs(ui): screenshots for Phase 4"
```

---

## Self-review

- [ ] Re-read the spec §Phase 3, Track A and Track B; confirm each item maps to a task.
- [ ] All vitest tests pass: `cd web && npm test`
- [ ] `cd web && npm run build` clean.
- [ ] Every shadcn primitive added has a test.
- [ ] No remaining `alert()` calls: `grep -rn "alert(" web/src/`
- [ ] No remaining direct uses of pre-shadcn modal/drawer divs in pages.

---

## Handoff

Phase 3 deliverables hand off to:
- **Phase 4 docs** — uses the screenshots from Task 12 in README + quickstarts.
- **Phase 5b run-replay** — uses the `DropdownMenu` row-actions and `Sheet` integration added here.
