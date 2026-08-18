import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const gatewayTarget = process.env.VITE_GATEWAY_TARGET || 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      '/v1': {
		target: gatewayTarget,
        changeOrigin: true,
      },
      '/ws': {
		target: gatewayTarget.replace(/^http/, 'ws'),
        ws: true,
      },
    },
    // Fix CSP issue for development
    headers: {
      'Content-Security-Policy': "script-src 'self' 'unsafe-eval' 'unsafe-inline'; style-src 'self' 'unsafe-inline'",
    },
  },
})
