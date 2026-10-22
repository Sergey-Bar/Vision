import { cn } from '@/lib/utils'

// The segmented control.
//
// This is the console's replacement for a row of tabs, and it is here because
// the design system's rule is "at most one filled element per screen": a row
// of tabs fills every tab it can, and the screen then has six filled things
// and no emphasis.
//
// A segmented control is a set of MUTUALLY EXCLUSIVE OPTIONS in one track.
// The selected one is the filled one — the screen's single filled element —
// and the rest are labels. It reads as a control rather than as navigation,
// which is what it is: it chooses what the region below shows, and the
// region below does not change the page.
//
// The word is always present. An icon-only segment is a segment whose meaning
// lives in a shape, and the shape is the thing this design system refuses.

export interface Segment<T extends string> {
  value: T
  label: string
  count?: number
}

export function SegmentedControl<T extends string>({
  segments,
  value,
  onChange,
  label,
  className,
}: {
  segments: Segment<T>[]
  value: T
  onChange: (value: T) => void
  label: string
  className?: string
}) {
  return (
    <div
      role="tablist"
      aria-label={label}
      className={cn(
        'inline-flex items-center gap-1 rounded-lg border border-line bg-bg-panel p-1',
        className,
      )}
    >
      {segments.map((s) => {
        const selected = s.value === value
        return (
          <button
            key={s.value}
            role="tab"
            type="button"
            aria-selected={selected}
            onClick={() => onChange(s.value)}
            className={cn(
              'inline-flex items-center gap-2 rounded px-3 py-1.5 text-[12px] font-medium',
              'transition-[background-color,color] duration-1 ease-out',
              selected
                ? 'bg-accent text-accent-fg'
                : 'text-text-muted hover:text-text hover:bg-bg-elevated',
            )}
          >
            {s.label}
            {s.count !== undefined && (
              <span className={cn('tabular-nums', selected ? 'text-accent-fg/70' : 'text-text-dim')}>
                {s.count}
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}
