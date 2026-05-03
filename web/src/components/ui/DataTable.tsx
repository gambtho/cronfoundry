import { cn } from '../../lib/cn'

/**
 * DataTable — the table chrome used everywhere data lives.
 * Encapsulates the column-header styling, row dividers, hover state,
 * and the failing-row red-bar pattern that grew up across Jobs and
 * JobDetail. Pages still own their own <td> markup; we only own the
 * shell.
 *
 *   <DataTable>
 *     <DataTable.Head>
 *       <DataTable.HeadCell>Name</DataTable.HeadCell>
 *       <DataTable.HeadCell align="right">Actions</DataTable.HeadCell>
 *     </DataTable.Head>
 *     <tbody>
 *       <DataTable.Row alert>...
 *         <DataTable.Cell>...</DataTable.Cell>
 *       </DataTable.Row>
 *     </tbody>
 *   </DataTable>
 */
interface DataTableProps {
  children: React.ReactNode
  className?: string
}

export function DataTable({ children, className }: DataTableProps) {
  return (
    <table
      className={cn(
        'w-full border-collapse bg-bg-2 font-mono text-[12px]',
        className,
      )}
    >
      {children}
    </table>
  )
}

function Head({ children }: { children: React.ReactNode }) {
  return (
    <thead>
      <tr className="border-y border-rule bg-bg-3 text-[10px] uppercase tracking-[0.16em] text-ink-3">
        {children}
      </tr>
    </thead>
  )
}

interface HeadCellProps {
  children?: React.ReactNode
  align?: 'left' | 'right'
  className?: string
}

function HeadCell({ children, align = 'left', className }: HeadCellProps) {
  return (
    <th
      scope="col"
      className={cn(
        'whitespace-nowrap px-4 py-2.5 font-medium',
        align === 'right' ? 'text-right' : 'text-left',
        className,
      )}
    >
      {children}
    </th>
  )
}

interface RowProps extends React.HTMLAttributes<HTMLTableRowElement> {
  /** Red left-border for rows that need attention. */
  alert?: boolean
  /** Dim the row (e.g. paused entities). */
  muted?: boolean
}

function Row({
  alert = false,
  muted = false,
  className,
  children,
  ...rest
}: RowProps) {
  return (
    <tr
      className={cn(
        'border-b border-rule transition-colors hover:bg-bg-3 last:border-b-0',
        alert && 'shadow-[inset_2px_0_0_var(--red)]',
        muted && 'opacity-60',
        className,
      )}
      {...rest}
    >
      {children}
    </tr>
  )
}

interface CellProps extends React.HTMLAttributes<HTMLTableCellElement> {
  align?: 'left' | 'right'
}

function Cell({ align = 'left', className, children, ...rest }: CellProps) {
  return (
    <td
      className={cn(
        'px-4 py-3 align-middle text-ink-2',
        align === 'right' && 'text-right',
        className,
      )}
      {...rest}
    >
      {children}
    </td>
  )
}

/** Centered loading/empty/error cell that spans every column. */
interface MessageProps {
  /** Number of columns to span. Required so the message lines up. */
  colSpan: number
  tone?: 'muted' | 'error'
  children: React.ReactNode
}

function Message({ colSpan, tone = 'muted', children }: MessageProps) {
  return (
    <tr>
      <td
        colSpan={colSpan}
        className={cn(
          'px-4 py-12 text-center font-mono text-[12px]',
          tone === 'error' ? 'text-accent-red' : 'text-ink-3',
        )}
      >
        {children}
      </td>
    </tr>
  )
}

DataTable.Head = Head
DataTable.HeadCell = HeadCell
DataTable.Row = Row
DataTable.Cell = Cell
DataTable.Message = Message
