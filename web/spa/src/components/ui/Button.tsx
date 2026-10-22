import { cn } from '@/lib/utils'

// The button.
//
// ONE filled element per screen, and this is the only thing that can be one.
// The console used to fill the active nav item, every primary button, the
// brand mark and the clock pill at the same time, so nothing on the screen
// was the emphasis; everything was.
//
// The variants are the three the design system allows:
//   - `primary`   the one filled accent on the screen
//   - `secondary` a hairline outline
//   - `ghost`     text only
//
// The size scale is two steps. A dense console needs a small control and a
// normal one, and a third is a decision nobody makes on purpose.

type Variant = 'primary' | 'secondary' | 'ghost'
type Size = 'sm' | 'md'

const VARIANTS: Record<Variant, string> = {
  primary:
    'bg-accent text-accent-fg border border-accent hover:bg-accent-strong hover:border-accent-strong',
  secondary:
    'bg-transparent text-text border border-line hover:border-line-strong hover:bg-bg-elevated hover:text-text',
  ghost:
    'bg-transparent text-text-muted border border-transparent hover:bg-bg-elevated hover:text-text',
}

const SIZES: Record<Size, string> = {
  sm: 'h-8 px-3 text-[12px] rounded',
  md: 'h-10 px-4 text-[13px] rounded',
}

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
}

export function Button({ variant = 'secondary', size = 'md', className, ...rest }: ButtonProps) {
  return (
    <button
      className={cn(
        'inline-flex items-center justify-center gap-2 font-medium tracking-tight',
        'transition-[background-color,border-color,color] duration-1 ease-out',
        'disabled:opacity-40 disabled:pointer-events-none',
        VARIANTS[variant],
        SIZES[size],
        className,
      )}
      {...rest}
    />
  )
}

// The icon button is a square with no label of its own, so it must carry one
// for the people who do not see the icon.
export interface IconButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  label: string
  size?: Size
}

export function IconButton({ label, size = 'md', className, ...rest }: IconButtonProps) {
  return (
    <button
      aria-label={label}
      title={label}
      className={cn(
        'inline-flex items-center justify-center text-text-muted',
        'border border-transparent rounded',
        'transition-[background-color,border-color,color] duration-1 ease-out',
        'hover:bg-bg-elevated hover:text-text',
        'disabled:opacity-40 disabled:pointer-events-none',
        size === 'sm' ? 'h-8 w-8' : 'h-10 w-10',
        className,
      )}
      {...rest}
    />
  )
}
