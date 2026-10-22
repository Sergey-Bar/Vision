// The mark.
//
// A hexagonal sensor chassis with a visor and a sensor node, on a 24x24 grid,
// drawn in the accent.
//
// It is the same shape as the showcase's mark, and it is the same shape for a
// reason: the demo and the console are two surfaces of ONE product, and a
// judge who moves from one to the other must not be told by the logo that
// they have arrived somewhere else.
//
// The console used to draw a red rounded square with a "V" in it. The red was
// the same red as "critical", so the mark announced an emergency before
// anything had happened, and it was a third product name in a box.
export function Mark({ size = 26 }: { size?: number }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      aria-hidden="true"
      style={{ color: 'var(--accent)', flex: 'none' }}
    >
      <path
        d="M12 2.5 20 6.6v6.2c0 4.9-3.3 8.3-8 9.7-4.7-1.4-8-4.8-8-9.7V6.6z"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
      <path d="M7.7 10.4h8.6" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      <circle cx="12" cy="15.1" r="1.5" fill="currentColor" />
    </svg>
  )
}

// The wordmark, and the one line that says what the product is.
//
// The statement is on every surface. It is the cheapest way to make a
// product memorable, and it is the same sentence everywhere so that a
// screenshot of the demo and a screenshot of the console are the same
// product.
export function Wordmark({ statement = true }: { statement?: boolean }) {
  return (
    <span className="flex items-center gap-3 min-w-0">
      <Mark />
      <span className="flex flex-col leading-none min-w-0">
        <span className="text-[13px] font-semibold tracking-[0.16em] text-text">VISION</span>
        {statement && (
          <span className="mt-1 text-[12px] text-text-dim truncate hidden md:block">
            The system that says what it does not know.
          </span>
        )}
      </span>
    </span>
  )
}
