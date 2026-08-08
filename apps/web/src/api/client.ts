import type {
  Account,
  AppConfig,
  KomootImportResult,
  KomootTour,
  LinkAccountRequest,
  Me,
  LibraryResponse,
  PlanResponse,
  PushResponse,
  Route,
  TrackResponse,
  UploadRequest,
} from './types'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: { Accept: 'application/json', ...(init?.headers ?? {}) },
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

  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

/** Slug segments are URL-safe already, but encode them so a stray space cannot break the path. */
function encodeSlug(slug: string): string {
  return slug.split('/').map(encodeURIComponent).join('/')
}

export const api = {
  config: () => request<AppConfig>('/api/config'),
  me: () => request<Me>('/api/me'),

  komootTours: () => request<KomootTour[]>('/api/komoot/tours'),
  komootImport: (tourIds: string[]) =>
    request<KomootImportResult>('/api/komoot/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tourIds }),
    }),

  accounts: () => request<Account[]>('/api/accounts'),
  linkAccount: (req: LinkAccountRequest) =>
    request<Account>('/api/accounts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),
  unlinkAccount: (id: string) =>
    request<void>(`/api/accounts/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  routes: () => request<LibraryResponse>('/api/routes'),
  plan: () => request<PlanResponse>('/api/plan'),
  track: (slug: string) => request<TrackResponse>(`/api/tracks/${encodeSlug(slug)}`),
  push: () => request<PushResponse>('/api/push', { method: 'POST' }),

  gpxUrl: (slug: string) => `/api/gpx/${encodeSlug(slug)}`,

  upload: (req: UploadRequest) => {
    const form = new FormData()
    form.append('file', req.file)
    if (req.name) form.append('name', req.name)
    if (req.description) form.append('description', req.description)
    if (req.tags) form.append('tags', req.tags)
    if (req.targets) form.append('targets', req.targets)
    if (req.uploadedBy) form.append('uploadedBy', req.uploadedBy)
    // No Content-Type header: the browser sets the multipart boundary.
    return request<Route>('/api/routes', { method: 'POST', body: form })
  },

  remove: (slug: string) =>
    request<void>(`/api/routes/${encodeSlug(slug)}`, { method: 'DELETE' }),
}
