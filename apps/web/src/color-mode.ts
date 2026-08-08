/**
 * Nuxt UI themes off a `dark` class on <html>; outside Nuxt, nothing puts it
 * there. This follows the OS preference and keeps following it, so the app
 * matches the rest of the machine — it gets looked at on a phone in a hallway
 * before a ride and on a laptop after one.
 */
export function followSystemColorScheme(): void {
  const query = window.matchMedia('(prefers-color-scheme: dark)')

  const apply = (dark: boolean) => {
    document.documentElement.classList.toggle('dark', dark)
    // Keeps form controls and scrollbars in step with the rest.
    document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
  }

  apply(query.matches)
  query.addEventListener('change', (event) => apply(event.matches))
}
