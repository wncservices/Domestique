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
  // two. Parallel workers are a different thing than retries, though — every
  // test here is fully independent (separate accounts or no account at all,
  // and Playwright gives each test its own isolated browser context by
  // default regardless of worker count), so running them at once only
  // shortens the wall-clock time the postPromotionAnalysis Job spends
  // waiting, it doesn't change what any test does or how many times it gets
  // to do it.
  fullyParallel: true,
  // One worker per test (there are 3, see tests/login.spec.ts) — small
  // enough to run all of them in a single batch instead of queueing, fixed
  // rather than left to Playwright's own CPU-count auto-detection, which
  // would just guess based on whatever this container happens to be
  // scheduled onto. Bump this (and the Job's own resources/dev-shm sizing
  // in domestique-infra's analysistemplate-post-promotion.yaml, sized and
  // tested for exactly 3 concurrent Chromium instances) if a test gets
  // added.
  workers: 3,
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
