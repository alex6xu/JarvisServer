/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        background: 'rgb(var(--color-background, 9 9 11) / <alpha-value>)',
        foreground: 'rgb(var(--color-foreground, 250 250 250) / <alpha-value>)',
        card: 'rgb(var(--color-card, 10 10 12) / <alpha-value>)',
        'card-foreground': 'rgb(var(--color-card-foreground, 250 250 250) / <alpha-value>)',
        primary: 'rgb(var(--color-primary, 59 130 246) / <alpha-value>)',
        'primary-foreground': 'rgb(var(--color-primary-foreground, 255 255 255) / <alpha-value>)',
        secondary: 'rgb(var(--color-secondary, 30 30 46) / <alpha-value>)',
        'secondary-foreground': 'rgb(var(--color-secondary-foreground, 161 161 170) / <alpha-value>)',
        muted: 'rgb(var(--color-muted, 24 24 27) / <alpha-value>)',
        'muted-foreground': 'rgb(var(--color-muted-foreground, 113 113 122) / <alpha-value>)',
        accent: 'rgb(var(--color-accent, 30 30 46) / <alpha-value>)',
        'accent-foreground': 'rgb(var(--color-accent-foreground, 250 250 250) / <alpha-value>)',
        destructive: 'rgb(var(--color-destructive, 239 68 68) / <alpha-value>)',
        success: 'rgb(var(--color-success, 34 197 94) / <alpha-value>)',
        warning: 'rgb(var(--color-warning, 245 158 11) / <alpha-value>)',
        border: 'rgb(var(--color-border, 39 39 42) / <alpha-value>)',
        ring: 'rgb(var(--color-ring, 59 130 246) / <alpha-value>)',
      },
      borderRadius: {
        DEFAULT: '0.625rem',
      },
    },
  },
  plugins: [],
}
