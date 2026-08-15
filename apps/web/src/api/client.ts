import type {
  Account,
  AppConfig,
  AssignableRole,
  GarminConnection,
  GarminConsumer,
  GarminCourse,
  GarminCourseImportResult,
  GarminDuplicateGroup,
  GarminDevice,
  KomootImportResult,
  KomootConnection,
  KomootTour,
  LinkAccountRequest,
  Me,
  InvitePersonRequest,
  LibraryResponse,
  Person,
  PlanResponse,
  PushResponse,
  Route,
  TrackResponse,
  UploadRequest,
} from './types'

/**
 * A failed request, carrying what the API said rather than only how it read.
 *
 * The body matters when a failure is a *state* and not just a message: a
 * Garmin sign-in refused for two-factor needs different words from a wrong
 * password, and matching on the text of an error message across the API
 * boundary is a thing that breaks the next time the wording is improved.
 */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly body: Record<string, unknown> = {},
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: { Accept: 'application/json', ...(init?.headers ?? {}) },
    ...init,
  })

  if (!response.ok) {
    // The API returns {"error": "..."} on failure; fall back to the status text
    // when something upstream (a proxy, a crash) returns something else.
    let detail = response.statusText
    let body: Record<string, unknown> = {}
    try {
      body = (await response.json()) as Record<string, unknown>
      if (typeof body.error === 'string') detail = body.error
    } catch {
      /* not JSON — keep the status text */
    }
    throw new ApiError(detail || `request to ${path} failed`, response.status, body)
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
  /** Ends an authMode oidc session. The app holds that session and ends it
   *  itself — POST rather than a link, since signing out is a state change,
   *  not a navigation. redirectTo is the issuer's own end-session URL when
   *  it advertises one, empty otherwise; either way the caller navigates
   *  there afterward, since this fetch cannot itself carry the browser to a
   *  cross-origin page the way a plain link can. */
  logout: () => request<{ redirectTo: string }>('/sso/logout', { method: 'POST' }),

  komootConnection: () => request<KomootConnection>('/api/komoot/connection'),
  komootConnect: (email: string, password: string) =>
    request<KomootConnection>('/api/komoot/connection', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    }),
  komootDisconnect: () =>
    request<KomootConnection>('/api/komoot/connection', { method: 'DELETE' }),

  garminConnection: () => request<GarminConnection>('/api/garmin/connection'),
  garminConnect: (email: string, password: string) =>
    request<GarminConnection>('/api/garmin/connection', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    }),
  garminDisconnect: () =>
    request<GarminConnection>('/api/garmin/connection', { method: 'DELETE' }),
  garminDevices: () => request<GarminDevice[]>('/api/garmin/devices'),

  garminCourses: () => request<GarminCourse[]>('/api/garmin/courses'),
  garminCourseImport: (courseIds: string[]) =>
    request<GarminCourseImportResult>('/api/garmin/courses/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ courseIds }),
    }),
  garminCourseDuplicates: () =>
    request<GarminDuplicateGroup[]>('/api/garmin/courses/duplicates'),
  garminCourseDelete: (id: string) =>
    request<{ status: string }>(`/api/garmin/courses/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),

  garminConsumer: () => request<GarminConsumer>('/api/garmin/consumer'),
  setGarminConsumer: (key: string, secret: string) =>
    request<GarminConsumer>('/api/garmin/consumer', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ key, secret }),
    }),
  clearGarminConsumer: () =>
    request<GarminConsumer>('/api/garmin/consumer', { method: 'DELETE' }),

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
  /** Omitting `items` (or passing all of them) pushes everything, same as
   *  before per-item selection existed. */
  push: (items?: { accountId: string; slug: string }[]) =>
    request<PushResponse>('/api/push', {
      method: 'POST',
      ...(items
        ? {
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ items }),
          }
        : {}),
    }),

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

  /** Always sends an explicit list — an empty one means "push nowhere", not
   *  "use the library default". The server has no way to ask for the default
   *  back through this field: `targets: null` decodes identically to the
   *  field being absent (Go's `encoding/json` collapses both into a nil
   *  pointer), which this endpoint already treats as "leave unchanged". */
  updateTargets: (slug: string, targets: string[]) =>
    request<Route>(`/api/routes/${encodeSlug(slug)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ targets }),
    }),

  people: () => request<Person[]>('/api/people'),
  invitePerson: (req: InvitePersonRequest) =>
    // The account can be created even when the invite email fails to send —
    // that is not an ApiError (the request itself succeeded), so the
    // response's own optional `error` field carries it instead.
    request<{ person: Person; error?: string }>('/api/people', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),
  setPersonRole: (id: string, role: AssignableRole) =>
    request<{ status: string }>(`/api/people/${encodeURIComponent(id)}/role`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ role }),
    }),
}
