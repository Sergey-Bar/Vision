/** @type {import('tailwindcss').Config} */

// ONE DESIGN SYSTEM.
//
// This file used to be a second answer to a question the CSS variable layer had
// already answered: Netflix black and Apple red here, a blue accent on a
// near-black ground there, and the pages reaching for whichever they happened
// to want. Two token sets in one product is two answers, and the second one is
// always the one nobody remembers to update.
//
// The token file is `web/static/tokens.css`, and it is read by BOTH surfaces:
// the showcase links it and the console's `index.css` imports it. This config
// is the Tailwind-side TRANSLATION of that file, not a second palette — every
// value here is a `var(--…)` reference, so changing a colour changes it
// everywhere and there is nothing here to keep in step.
//
// The opacity modifiers are why this is not a one-liner. Tailwind applies an
// alpha to a colour by writing `rgb(var(--x) / <alpha-value>)`, and a variable
// holding a hex cannot be alpha'd that way. `color-mix` can: it takes the
// variable and mixes it with transparent.

/**
 * A colour token, read from the CSS variable layer.
 *
 * The opacity modifier is why this is not a one-liner. Tailwind applies an
 * alpha to a colour by writing `rgb(var(--x) / <alpha-value>)`, and a variable
 * holding a hex cannot be alpha'd that way. `color-mix` can: it takes the
 * variable and mixes it with transparent.
 *
 * The `Number.isFinite` guard is not defensive decoration. Tailwind hands
 * the base utility an opacity value that is not a number, and the old helper
 * turned it into `color-mix(in srgb, var(--color-text) NaN%, transparent)` —
 * an invalid declaration, which the browser DROPS. Every muted and dim
 * colour in the console was therefore being discarded and inherited from its
 * parent, so the whole screen was one shade of white. The console looked flat
 * and cheap because half its colour rules were not being applied at all.
 */
const token = (name) => ({ opacityValue }) => {
  const alpha = Number.parseFloat(opacityValue)
  if (!Number.isFinite(alpha)) return `var(--color-${name})`
  return `color-mix(in srgb, var(--color-${name}) ${alpha * 100}%, transparent)`
}

export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // The ground and its steps. The showcase calls them --bg, --bg-panel
        // and --bg-elevated; the console calls them bg, surface and
        // surface-lift. Same values, two vocabularies, one palette.
        bg: token('bg'),
        'bg-panel': token('bg-panel'),
        'bg-elevated': token('bg-elevated'),
        'bg-hover': token('bg-hover'),
        surface: token('bg-panel'),
        'surface-lift': token('bg-elevated'),
        'surface-glass': token('bg-elevated'),

        // The hairlines.
        //
        // `line` is the name the console's own classes use -- `border-line`,
        // `divide-line`, `bg-line` -- and it was MISSING here until the colour
        // gate caught it. The config declared `border` and `line-strong`, so
        // `border-line-strong` resolved and `border-line` did not: every
        // hairline in the console compiled, rendered nothing, and failed
        // silently. A palette that is missing one of its own names takes the
        // screen's structure with it.
        line: token('line'),
        'line-strong': token('line-strong'),
        border: token('line'),
        'border-hover': token('line-strong'),

        // The ink.
        text: token('text'),
        'text-muted': token('text-muted'),
        'text-dim': token('text-dim'),

        // The one accent, and its foreground. A filled accent uses
        // `accent-fg`, never white: white on this blue is 3.4:1.
        accent: token('accent'),
        'accent-strong': token('accent-strong'),
        'accent-fg': token('accent-fg'),
        'accent-ink': token('accent-ink'),

        // The four states, and their inks. A state's FILL is its dot; a
        // state's INK is its word.
        success: token('success'),
        'ok-ink': token('ok-ink'),
        warning: token('warning'),
        'warn-ink': token('warn-ink'),
        danger: token('danger'),
        'bad-ink': token('bad-ink'),
        unknown: token('unknown'),
        'unknown-ink': token('unknown-ink'),
      },
      // The radii are the CSS variable layer's, for the same reason the colours
      // are: a radius scale in two places is two scales, and the one nobody
      // updates is the one that drifts.
      //
      // Every step is in the scale, including the ones the pages used that
      // Tailwind never generated — `rounded-2xl` and `rounded-3xl` were 16px
      // and 24px on a design that caps its radii at 14px, so the whole console
      // was built out of consumer-app corners. They are mapped now, and the
      // names the pages already use keep working.
      borderRadius: {
        none: '0px',
        DEFAULT: 'var(--radius)',
        sm: 'var(--radius-sm)',
        md: 'var(--radius)',
        lg: 'var(--radius-lg)',
        xl: 'var(--radius-lg)',
        '2xl': 'var(--radius-lg)',
        '3xl': 'var(--radius-lg)',
        '4xl': 'var(--radius-xl)',
        full: 'var(--radius-full)',
      },
      fontFamily: {
        sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'Roboto', 'Helvetica', 'Arial', 'sans-serif'],
        mono: ['JetBrains Mono', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'Consolas', 'monospace'],
      },
      // The type scale, from the token file. The old names (`xs`, `sm`,
      // `base`, `lg`) are kept and point at the same sizes, because the pages
      // were written against them and a size scale that has two spellings is
      // a size scale that drifts.
      fontSize: {
        'display': ['var(--fs-display)', { lineHeight: 'var(--lh-display)', letterSpacing: 'var(--tracking-display)' }],
        'title': ['var(--fs-title)', { lineHeight: 'var(--lh-title)', letterSpacing: 'var(--tracking-title)' }],
        'heading': ['var(--fs-heading)', { lineHeight: 'var(--lh-heading)', letterSpacing: 'var(--tracking-heading)' }],
        'subhead': ['var(--fs-subhead)', { lineHeight: 'var(--lh-subhead)', letterSpacing: 'var(--tracking-subhead)' }],
        'body': ['var(--fs-body)', { lineHeight: 'var(--lh-body)' }],
        'body-sm': ['var(--fs-body-sm)', { lineHeight: 'var(--lh-body-sm)' }],
        'meta': ['var(--fs-meta)', { lineHeight: 'var(--lh-meta)' }],
        'micro': ['var(--fs-micro)', { lineHeight: 'var(--lh-micro)', letterSpacing: 'var(--tracking-micro)' }],
        'xs': ['var(--fs-micro)', { lineHeight: 'var(--lh-micro)' }],
        'sm': ['var(--fs-body-sm)', { lineHeight: 'var(--lh-body-sm)' }],
        'base': ['var(--fs-body)', { lineHeight: 'var(--lh-body)' }],
        'lg': ['var(--fs-subhead)', { lineHeight: 'var(--lh-subhead)' }],
      },
      // Weights 400, 500 and 600. Nothing heavier.
      fontWeight: {
        normal: '400',
        medium: '500',
        semibold: '600',
        bold: '600',
        extrabold: '600',
        black: '600',
      },
      // A four-pixel grid, in pixels rather than in rem, so it does not move
      // when the root font size does. The console is dense on purpose.
      spacing: {
        0: '0px',
        1: '4px',
        2: '8px',
        3: '12px',
        4: '16px',
        5: '20px',
        6: '24px',
        7: '28px',
        8: '32px',
        10: '40px',
        12: '48px',
        14: '56px',
        16: '64px',
        20: '80px',
        24: '96px',
        32: '128px',
        // The DENSITY scale. `cell` is a region's cell padding and `row` is a
        // region's row height, and both are tokens rather than sizes, so the
        // compact density is a change to two values in the token file rather
        // than a second set of class names.
        cell: 'var(--cell-pad)',
      },
      // The heights the app bar and the rail use, named so that a class that
      // Tailwind never generated is a class that fails a gate instead of a
      // class that silently does nothing.
      height: {
        bar: '56px',
        rail: '56px',
        stage: 'calc(100vh - 56px)',
        row: 'var(--row-h)',
      },
      // The 16-tile density the live grid renders. `lg:grid-cols-16` is not
      // in Tailwind's default scale, so the grid silently fell back to four
      // columns and the "16 tiles" were four tiles.
      gridTemplateColumns: {
        13: 'repeat(13, minmax(0, 1fr))',
        14: 'repeat(14, minmax(0, 1fr))',
        15: 'repeat(15, minmax(0, 1fr))',
        16: 'repeat(16, minmax(0, 1fr))',
      },
      // Depth is a tint step and a hairline edge. The thirteen glow shadows
      // this file used to declare are gone: on a dark ground a glow is a
      // smudge, and the console had `shadow-2xl` on nearly every card.
      boxShadow: {
        none: 'none',
        edge: 'inset 0 1px 0 rgb(255 255 255 / 4%)',
      },
      transitionDuration: {
        1: '120ms',
        2: '200ms',
        3: '320ms',
        4: '520ms',
      },
      transitionTimingFunction: {
        out: 'cubic-bezier(0.16, 1, 0.3, 1)',
      },
      keyframes: {
        'fade-in': {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        'slide-up': {
          '0%': { opacity: '0', transform: 'translateY(8px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
      animation: {
        'fade-in': 'fade-in var(--dur-2) var(--ease-out)',
        'slide-up': 'slideUp var(--dur-3) var(--ease-out)',
      },
    },
  },
  plugins: [],
}
