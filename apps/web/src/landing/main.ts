import { initColorMode } from '../color-mode'

// Enhances landing.html's static markup; renders nothing itself. See that
// file's own comment for why the markup has to be real HTML from the first
// byte rather than a mount point this used to be.

// Where the app lives. Built in, so a self-hoster changes it in one place —
// this page cannot discover its own deployment's domain at runtime.
const appHost = import.meta.env.VITE_APP_URL ?? 'https://app.domestique.dev'

const signInLinks = document.querySelectorAll<HTMLAnchorElement>('[data-signin]')
function setSignInHref(href: string) {
  signInLinks.forEach((el) => {
    el.href = href
  })
}
// The static markup's own href, matched here rather than left to whatever
// was hand-typed in the HTML: the safe default until the upgrade below
// resolves, or forever if it doesn't.
setSignInHref(appHost)

// Where "Sign in" actually goes depends on the auth mode, which this page
// cannot know at build time: the same published image runs any mode, so
// baking a mode-specific path in here would be right for this deployment
// and wrong for every self-hoster who just pulls the image rather than
// building their own.
//
// mode: proxy wants the plain cross-origin link already set above —
// Authelia sits in front of it and shows its own login form. mode: oidc
// wants /sso/login appended instead: the app verifies tokens itself, and a
// bare visit to its root now redirects straight back to this page (see
// spaHandler in apps/api/internal/api/server.go), so the plain link
// bounces. The default above is the safe one: worst case before this
// resolves is one extra bounce through the app's own redirect, not a
// broken link — and if it never resolves at all (JS disabled, a network
// hiccup), that bounce is all a visitor ever sees.
fetch('/api/me')
  .then((res) => (res.ok ? res.json() : null))
  .then((me) => {
    if (me?.authMode === 'oidc') setSignInHref(`${appHost}/sso/login`)
  })
  .catch(() => {
    // Network hiccup, or a backend old enough to have no /api/me: keep the
    // proxy-shaped default rather than break the link over it.
  })

// Unlike the old Vue-mounted version of this page, nothing here takes
// ownership of <html>'s attributes after this runs, so — unlike main.ts for
// the app itself — one call is enough.
initColorMode()
