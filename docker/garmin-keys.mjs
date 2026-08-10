// Installs the Garmin app keys into a locally running Domestique.
//
// `just garmin-keys`. It is the local stand-in for the cluster's weekly
// CronJob: same source, same validation, same endpoint. Without it, getting a
// Garmin sign-in form on a fresh database means fetching a JSON file by hand
// and copying two values into the Settings page after every `just reset`.
//
// Node rather than a shell script with curl and jq: the `node` service is
// already in compose, already on the app's network, and Node 24 has fetch and
// JSON built in. One less image to pull and one less thing to install.
//
// This is a **developer command**, run deliberately. The app itself still
// refuses to fetch this at startup — see AGENTS.md — because a service that
// downloads its own signing key from a third-party bucket on every boot is a
// dependency nobody reviewed.

const SOURCE =
  process.env.GARMIN_CONSUMER_URL ?? 'https://thegarth.s3.amazonaws.com/oauth_consumer.json'
// `app` is the compose service name; from the host it would be localhost:8080.
const APP = process.env.DOMESTIQUE_URL ?? 'http://app:8080'

/** A short hash, so output can say "which pair" without printing a credential. */
async function fingerprint(value) {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value))
  return [...new Uint8Array(digest)]
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
    .slice(0, 12)
}

/**
 * The same check the cluster job makes. A bad answer from the bucket should
 * fail loudly here rather than turn into a sign-in that mysteriously does not
 * work.
 */
function plausible(value) {
  return typeof value === 'string' && /^[A-Za-z0-9._~-]{16,128}$/.test(value)
}

function die(message) {
  console.error(`✗ ${message}`)
  process.exit(1)
}

console.log(`Fetching Garmin app keys from ${SOURCE}`)

let payload
try {
  const response = await fetch(SOURCE, { signal: AbortSignal.timeout(30_000) })
  if (!response.ok) die(`${SOURCE} returned ${response.status}`)
  payload = await response.json()
} catch (err) {
  die(`could not fetch the keys: ${err.message}`)
}

const key = payload.consumer_key
const secret = payload.consumer_secret
if (!plausible(key) || !plausible(secret)) {
  die('that response is not a usable consumer pair; refusing to install it')
}
console.log(`  key ${await fingerprint(key)}, secret ${await fingerprint(secret)}`)

let result
try {
  result = await fetch(`${APP}/api/garmin/consumer`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ key, secret }),
    signal: AbortSignal.timeout(15_000),
  })
} catch (err) {
  die(`could not reach Domestique at ${APP} — is it running? (\`just up\`)\n  ${err.message}`)
}

if (!result.ok) {
  const body = await result.json().catch(() => ({}))
  die(`Domestique refused them (${result.status}): ${body.error ?? result.statusText}`)
}

console.log('✓ Installed. The Garmin sign-in form is now on the Settings page.')
console.log('  They live in the local database, so they survive `just down` but not `just reset`.')
