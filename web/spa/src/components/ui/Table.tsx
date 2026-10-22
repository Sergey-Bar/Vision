import { cn } from '@/lib/utils'

// The table.
//
// One table style, and it is the showcase's table style: a hairline under the
// header row, a hairline between rows, no zebra, no vertical rules, and the
// numbers aligned. A dense console is read by scanning a column, and a zebra
// stripe is a stripe that makes the column harder to follow.
//
// Every cell is a cell. The header is a header, and it says what the column
// holds.

export interface Column<T> {
  key: string
  header: React.ReactNode
  className?: string
  numeric?: boolean
  render: (row: T) => React.ReactNode
}

export function Table<T>({
  columns,
  rows,
  rowKey,
  onRowClick,
  empty,
  className,
}: {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T, index: number) => string
  onRowClick?: (row: T) => void
  empty?: React.ReactNode
  className?: string
}) {
  if (rows.length === 0 && empty) {
    return <>{empty}</>
  }
  return (
    <div className={cn('overflow-x-auto', className)}>
      <table className="w-full border-collapse text-[14px] leading-[20px]">
        <thead>
          <tr>
            {columns.map((c) => (
              <th
                key={c.key}
                scope="col"
                className={cn(
                  'border-b border-line-strong pb-2 pr-3 text-left font-mono text-[10px] font-semibold uppercase tracking-[0.08em] text-text-dim',
                  c.numeric && 'text-right',
                  c.className,
                )}
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={rowKey(row, rows.indexOf(row))}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
              className={cn(
                'border-b border-line last:border-b-0',
                onRowClick && 'cursor-pointer transition-colors duration-1 ease-out hover:bg-bg-elevated',
              )}
            >
              {columns.map((c) => (
                <td
                  key={c.key}
                  className={cn(
                    'py-3 pr-3 align-top text-text-muted',
                    c.numeric && 'text-right tabular-nums text-text',
                    c.className,
                  )}
                >
                  {c.render(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// A key/value pair, the shape most of a console's facts take.
export function KeyValue({
  items,
  className,
}: {
  items: { key: React.ReactNode; value: React.ReactNode }[]
  className?: string
}) {
  return (
    <dl className={cn('grid grid-cols-[132px_1fr] gap-x-4 gap-y-2 text-[13px] leading-[18px]', className)}>
      {items.map((item, i) => (
        <div key={i} className="contents">
          <dt className="font-mono text-[11px] uppercase tracking-[0.06em] text-text-dim">{item.key}</dt>
          <dd className="text-text-muted break-words">{item.value}</dd>
        </div>
      ))}
    </dl>
  )
}
