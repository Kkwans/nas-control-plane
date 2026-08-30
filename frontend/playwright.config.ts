import { defineConfig, devices } from '@playwright/test'

const configuredPort = Number.parseInt(process.env.NCP_E2E_PORT || '14173', 10)
const e2ePort = Number.isInteger(configuredPort) && configuredPort > 1024 && configuredPort < 65536 ? configuredPort : 14173
const e2eURL = `http://127.0.0.1:${e2ePort}`

export default defineConfig({
  testDir: './e2e',
  // NAS-local Chromium is resource constrained; keep CI deterministic while
  // allowing an explicit override for faster runners.
  workers: process.env.NCP_E2E_WORKERS
    ? Number.parseInt(process.env.NCP_E2E_WORKERS, 10)
    : process.env.CI
      ? 2
      : undefined,
  timeout: 60_000,
  expect: {
    // GitHub Chromium and the NAS acceptance Chromium rasterize fonts slightly
    // differently; keep local baselines strict and tolerate only small CI noise.
    toHaveScreenshot: { maxDiffPixelRatio: process.env.CI ? 0.02 : 0 },
  },
  use: {
    baseURL: e2eURL,
    trace: 'retain-on-failure',
  },
  webServer: {
    // Build once before the suite so lazy-route compilation cannot starve the
    // NAS-local Chromium process during a multi-page run. Preview still uses
    // the validated dedicated port and strict binding below.
    command: `corepack pnpm run build && corepack pnpm exec vite preview --host 127.0.0.1 --port ${e2ePort} --strictPort`,
    url: e2eURL,
    // Never attach to an unrelated service that happens to answer on the
    // configured port (for example NAS File Browser on 4173).
    reuseExistingServer: false,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
