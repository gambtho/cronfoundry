import type { Config } from 'tailwindcss'

/**
 * Tailwind theme references the CSS variables defined in src/index.css.
 * Single source of truth: tokens are CSS vars; this file just exposes
 * them as utility classes (bg-bg-2, text-ink, border-rule, etc.).
 */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      fontFamily: {
        sans: ['Geist', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        mono: ['Geist Mono', 'ui-monospace', 'monospace'],
      },
      colors: {
        bg: {
          DEFAULT: 'var(--bg)',
          2: 'var(--bg-2)',
          3: 'var(--bg-3)',
          4: 'var(--bg-4)',
        },
        rule: {
          DEFAULT: 'var(--rule)',
          2: 'var(--rule-2)',
        },
        ink: {
          DEFAULT: 'var(--ink)',
          2: 'var(--ink-2)',
          3: 'var(--ink-3)',
          4: 'var(--ink-4)',
        },
        accent: {
          green: 'var(--green)',
          'green-dim': 'var(--green-dim)',
          red: 'var(--red)',
          'red-dim': 'var(--red-dim)',
          amber: 'var(--amber)',
          cyan: 'var(--cyan)',
        },
      },
      backgroundColor: {
        'green-bg': 'var(--green-bg)',
        'red-bg': 'var(--red-bg)',
        'amber-bg': 'var(--amber-bg)',
      },
      borderRadius: {
        // Hardware-edge feel — keep corners tight.
        DEFAULT: '3px',
      },
      keyframes: {
        pulse: {
          '0%, 100%': { opacity: '1' },
          '50%': { opacity: '0.4' },
        },
      },
      animation: {
        pulse: 'pulse 1.6s infinite',
      },
    },
  },
  plugins: [],
} satisfies Config
