import { cn } from '@/lib/utils'

// The form controls.
//
// Every control has a real `<label for>`. The console had ZERO `htmlFor` in
// the whole application, which means every input in it was announced by its
// placeholder and nothing else — and a placeholder disappears the moment
// somebody types.
//
// The label is a real element, the hint is real text, and the error is
// announced.

export function Field({
  label,
  hint,
  htmlFor,
  error,
  children,
  className,
}: {
  label: React.ReactNode
  hint?: React.ReactNode
  htmlFor: string
  error?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <label htmlFor={htmlFor} className="text-[12px] font-medium text-text-muted">
        {label}
      </label>
      {children}
      {hint && !error && <p className="text-[12px] leading-[18px] text-text-dim">{hint}</p>}
      {error && (
        <p id={`${htmlFor}-error`} role="alert" className="text-[12px] leading-[18px] text-bad-ink">
          <span aria-hidden="true">■ </span>
          {error}
        </p>
      )}
    </div>
  )
}

const CONTROL =
  'w-full rounded border border-line-strong bg-bg px-3 py-2 text-[14px] leading-[20px] ' +
  'text-text placeholder:text-text-dim transition-colors duration-1 ease-out ' +
  'focus:border-accent focus:outline-none'

export function Input({ className, ...rest }: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cn(CONTROL, 'h-10', className)} {...rest} />
}

export function Textarea({ className, ...rest }: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn(CONTROL, 'min-h-[96px] resize-y', className)} {...rest} />
}

export function Select({ className, children, ...rest }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={cn(CONTROL, 'h-10 pr-8', className)} {...rest}>
      {children}
    </select>
  )
}

export function Checkbox({
  label,
  id,
  className,
  ...rest
}: React.InputHTMLAttributes<HTMLInputElement> & { label: React.ReactNode }) {
  return (
    <label htmlFor={id} className={cn('inline-flex items-center gap-2.5 text-[13px] text-text-muted', className)}>
      <input
        id={id}
        type="checkbox"
        className="h-4 w-4 shrink-0 rounded border border-line-strong bg-bg accent-[var(--accent)]"
        {...rest}
      />
      {label}
    </label>
  )
}

export function RadioGroup({
  legend,
  name,
  options,
  value,
  onChange,
}: {
  legend: React.ReactNode
  name: string
  options: { value: string; label: React.ReactNode; hint?: React.ReactNode }[]
  value: string
  onChange: (value: string) => void
}) {
  return (
    <fieldset className="border-0 p-0">
      <legend className="mb-2 text-[12px] font-medium text-text-muted">{legend}</legend>
      <div className="flex flex-col gap-2.5">
        {options.map((o) => (
          <label
            key={o.value}
            htmlFor={`${name}-${o.value}`}
            className="flex items-start gap-2.5 text-[13px] leading-[20px] text-text-muted"
          >
            <input
              id={`${name}-${o.value}`}
              type="radio"
              name={name}
              value={o.value}
              checked={value === o.value}
              onChange={() => onChange(o.value)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-[var(--accent)]"
            />
            <span>
              {o.label}
              {o.hint && <span className="block text-[12px] text-text-dim">{o.hint}</span>}
            </span>
          </label>
        ))}
      </div>
    </fieldset>
  )
}

// The switch, for a setting that is on or off and whose state is visible on
// the switch itself. It is a button with `aria-pressed`, not a checkbox, so
// its state is announced as a state.
export function Switch({
  checked,
  onChange,
  label,
}: {
  checked: boolean
  onChange: (checked: boolean) => void
  label: string
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative h-6 w-11 rounded-full border transition-colors duration-1 ease-out',
        checked ? 'border-accent bg-accent' : 'border-line-strong bg-bg-elevated',
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          'absolute top-0.5 h-4 w-4 rounded-full transition-[left] duration-1 ease-out',
          checked ? 'left-[22px] bg-accent-fg' : 'left-0.5 bg-text-dim',
        )}
      />
    </button>
  )
}
