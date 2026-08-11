/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        background: '#09090b',
        foreground: '#fafafa',
        card: '#0a0a0c',
        'card-foreground': '#fafafa',
        primary: '#3b82f6',
        'primary-foreground': '#ffffff',
        secondary: '#1e1e2e',
        'secondary-foreground': '#a1a1aa',
        muted: '#18181b',
        'muted-foreground': '#71717a',
        accent: '#1e1e2e',
        'accent-foreground': '#fafafa',
        destructive: '#ef4444',
        success: '#22c55e',
        warning: '#f59e0b',
        border: '#27272a',
        ring: '#3b82f6',
      },
      borderRadius: {
        DEFAULT: '0.625rem',
      },
    },
  },
  plugins: [],
}
