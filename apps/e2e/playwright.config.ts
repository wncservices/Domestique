import { defineConfig } from '@playwright/test'

// Deliberately its own package, not part of apps/web's npm workspace: this
// runs against a *deployed* URL from inside its own container image (see
// Dockerfile), as an Argo Rollouts AnalysisRun job — it never gets bundled
// with the app, and pulling Playwright's browser download into apps/web's
// own dependency tree would be dead weight for everyone who is not running
// this.
export default defineConfig({
  testDir: './tests',
  timeout: 60_000,
  retries: 0,
  // One retry-free attempt: this is a promotion gate reacting to something
  // that either works or does not, not a flaky test suite to smooth over —
  // a real failure here should abort/roll back, not quietly pass on attempt
  // two.
  fullyParallel: false,
  reporter: [['list']],
  use: {
    baseURL: process.env.DOMESTIQUE_BASE_URL ?? 'https://app.domestique.dev',
    trace: 'retain-on-failure',
  },
})
