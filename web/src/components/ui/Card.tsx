import { cn } from '../../lib/cn'

/**
 * Card — bordered surface used everywhere data lives.
 * Header is optional; when present it gets a darker fill and uppercase mono label.
 *
 *   <Card>
 *     <Card.Header>Provider <Card.HeaderMeta>9 jobs</Card.HeaderMeta></Card.Header>
 *     <Card.Body>...</Card.Body>
 *   </Card>
 */
interface CardProps extends React.HTMLAttributes<HTMLElement> {
  children: React.ReactNode
}

export function Card({ children, className, ...rest }: CardProps) {
  return (
    <section
      className={cn(
        'mb-4 overflow-hidden rounded border border-rule bg-bg-2',
        className,
      )}
      {...rest}
    >
      {children}
    </section>
  )
}

function CardHeader({ children, className, ...rest }: CardProps) {
  return (
    <header
      className={cn(
        'flex items-center gap-3.5 border-b border-rule bg-bg-3 px-4 py-2.5',
        'font-mono text-[10px] uppercase tracking-[0.16em] text-ink-2',
        className,
      )}
      {...rest}
    >
      {children}
    </header>
  )
}

function CardHeaderMeta({ children, className }: CardProps) {
  return (
    <span
      className={cn(
        'ml-auto font-mono normal-case tracking-normal text-ink-3',
        className,
      )}
    >
      {children}
    </span>
  )
}

function CardBody({ children, className, ...rest }: CardProps) {
  return (
    <div className={cn('px-4 py-3.5', className)} {...rest}>
      {children}
    </div>
  )
}

Card.Header = CardHeader
Card.HeaderMeta = CardHeaderMeta
Card.Body = CardBody
