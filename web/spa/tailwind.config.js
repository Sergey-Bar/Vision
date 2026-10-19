/** @type {import('tailwindcss').Config} */

// ONE DESIGN SYSTEM.
//
// This file used to be a second answer to a question the CSS variable layer had
// already answered: navy surfaces and a sky-blue accent here, Netflix black and
// Apple red there, and the pages reaching for whichever they happened to want.
// Two token sets in one product is two answers, and the second one is always
// the one nobody remembers to update.
//
// The CSS variable layer in `src/index.css` is the one that survives, because
// it is the one the pages were written against. This file reads it rather than
// restating it: a token is a `var(--color-…)` reference, so changing a colour
// changes it everywhere and there is nothing here to keep in step.
//
// The opacity modifiers are why this is not a one-liner. Tailwind applies an
// alpha to a colour by writing `rgb(var(--x) / <alpha-value>)`, and a variable
// holding a hex cannot be alpha'd that way. `color-mix` can: it takes the
// variable and mixes it with transparent.

/**
 * A colour token, read from the CSS variable layer.
 *
 * @param {string} name the variable's suffix, without the `--color-` prefix
 */
const token = (name) => ({ opacityValue }) =>
  opacityValue === undefined
    ? `var(--color-${name})`
    : `color-mix(in srgb, var(--color-${name}) ${Number(opacityValue) * 100}%, transparent)`

/**
 * A glow in a token's colour.
 *
 * The suffix is the blur in pixels AND the alpha in percent, which is the
 * convention the hand-written shadows used and the one the pages' class names
 * already follow: `shadow-apple-red/50` is a 50-pixel glow at half strength.
 *
 * @param {string} name the colour token
 * @param {number} pixels the blur radius, and the alpha percentage
 */
const glow = (name, pixels) => `0 0 ${pixels}px ${token(name)({ opacityValue: pixels / 100 })}`

export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        bg: token('bg'),
        'bg-deep': token('bg-deep'),
        surface: token('surface'),
        'surface-lift': token('surface-lift'),
        'surface-glass': token('surface-glass'),
        border: token('border'),
        'border-hover': token('border-hover'),
        text: token('text'),
        'text-muted': token('text-muted'),
        'text-dim': token('text-dim'),
        accent: token('accent'),
        'accent-hover': token('accent-hover'),
        'accent-glow': token('accent-glow'),
        success: token('success'),
        warning: token('warning'),
        danger: token('danger'),
        'danger-bg': token('danger-bg'),
        'apple-red': token('apple-red'),
        'apple-red-hover': token('apple-red-hover'),
        'apple-green': token('apple-green'),
        'apple-green-hover': token('apple-green-hover'),
        'apple-yellow': token('apple-yellow'),
        'apple-yellow-hover': token('apple-yellow-hover'),
        'apple-blue': token('apple-blue'),
        'apple-blue-hover': token('apple-blue-hover'),
      },
      // The radii are the CSS variable layer's, for the same reason the colours
      // are: a radius scale in two places is two scales, and the one nobody
      // updates is the one that drifts.
      borderRadius: {
        DEFAULT: 'var(--radius)',
        sm: 'var(--radius-sm)',
        lg: 'var(--radius-lg)',
        xl: 'var(--radius-xl)',
        full: 'var(--radius-full)',
      },
      fontFamily: {
        sans: ['Inter', 'SF Pro Text', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'Roboto', 'Helvetica', 'Arial', 'sans-serif'],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace'],
      },
      fontSize: {
        xs: ['11px', { lineHeight: '1.5' }],
        sm: ['13px', { lineHeight: '1.5' }],
        base: ['14px', { lineHeight: '1.5' }],
        lg: ['16px', { lineHeight: '1.5' }],
      },
      // A tight scale, in pixels rather than in rem, so it does not move when
      // the root font size does. The console is dense on purpose.
      spacing: {
        0: '0px',
        1: '4px',
        2: '8px',
        3: '12px',
        4: '16px',
        5: '20px',
        6: '24px',
        8: '32px',
        10: '40px',
        12: '48px',
        16: '64px',
        20: '80px',
        24: '96px',
      },
      boxShadow: {
        glass: '0 10px 30px -10px rgba(0, 0, 0, 0.6)',
        'accent/30': glow('accent', 30),
        'accent/40': glow('accent', 40),
        'accent/50': glow('accent', 50),
        'apple-red/20': glow('apple-red', 20),
        'apple-red/30': glow('apple-red', 30),
        'apple-red/40': glow('apple-red', 40),
        'apple-red/50': glow('apple-red', 50),
        'apple-green/20': glow('apple-green', 20),
        'apple-green/30': glow('apple-green', 30),
        'danger/20': glow('danger', 20),
        'danger/30': glow('danger', 30),
        'success/20': glow('success', 20),
        'success/40': glow('success', 40),
        'warning/20': glow('warning', 20),
      },
      animation: {
        'fade-in': 'fadeIn 0.2s ease-out',
        'slide-up': 'slideUp 0.3s ease-out',
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(8px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
    },
  },
  plugins: [],
}
