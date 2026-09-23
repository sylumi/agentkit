import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Preserve the browser Host so the API's same-origin write check still applies.
const proxy = {
  '/api': { target: process.env.AGENTKIT_API_URL || 'http://127.0.0.1:8081', changeOrigin: false },
}

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: './',
  server: { proxy },
  preview: { proxy },
  resolve: { alias: { '@': new URL('./src', import.meta.url).pathname } },
})
