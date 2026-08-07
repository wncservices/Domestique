import type {
  Account,
  LibraryResponse,
  PlanResponse,
  PushResponse,
  TrackResponse,
} from './types'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: { Accept: 'application/json' },
    ...init,
  })

  if (!response.ok) {
    // The API returns {"error": "..."} on failure; fall back to the status text
    // when something upstream (a proxy, a crash) returns something else.
    let detail = response.statusText
    try {
      const body = (await response.json()) as { error?: string }
      if (body.error) detail = body.error
    } catch {
      /* not JSON — keep the status text */
    }
    throw new Error(detail || `request to ${path} failed`)
  }

  return (await response.json()) as T
}

export const api = {
  accounts: () => request<Account[]>('/api/accounts'),
  routes: () => request<LibraryResponse>('/api/routes'),
  plan: () => request<PlanResponse>('/api/plan'),
  // Slug segments are already URL-safe (they are directory names), but encode
  // each one anyway so a stray space or accent cannot break the request.
  track: (slug: string) =>
    request<TrackResponse>(
      `/api/tracks/${slug.split('/').map(encodeURIComponent).join('/')}`,
    ),
  push: () => request<PushResponse>('/api/push', { method: 'POST' }),
}
