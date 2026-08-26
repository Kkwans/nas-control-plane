import { defineConfig, devices } from '@playwright/test'

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
    baseURL: 'http://127.0.0.1:4173',
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'corepack pnpm dev --host 127.0.0.1',
    url: 'http://127.0.0.1:4173',
    reuseExistingServer: !process.env.CI,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
