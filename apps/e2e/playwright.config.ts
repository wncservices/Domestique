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
  // Every request this suite makes to a container the pod itself never
  // persists anywhere (a one-shot Job with no artifact export wired up),
  // but /app is read-only in that container and Playwright's own trace/
  // screenshot output has to land somewhere real — the pod mounts /tmp
  // writable for exactly this.
  outputDir: '/tmp/test-results',
  use: {
    baseURL: process.env.DOMESTIQUE_BASE_URL ?? 'https://app.domestique.dev',
    // No trace, even on failure: a Playwright trace records full network
    // request bodies, and the Auth0 login POST this suite drives carries
    // test-admin's real password in cleartext in that body. Nothing
    // exports this Job's filesystem today, so it isn't a live leak, but a
    // trace file is the kind of thing someone adds artifact upload for
    // later while debugging a failing gate, and a password-bearing trace
    // landing somewhere less protected than Vault is not a risk worth
    // taking for a debugging convenience. A failure screenshot carries
    // none of that risk (password inputs render masked), and still shows
    // what page the run actually landed on.
    trace: 'off',
    screenshot: 'only-on-failure',
  },
})
