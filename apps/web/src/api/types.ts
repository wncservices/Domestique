// Mirrors the DTOs in apps/api/internal/api/server.go. Keep the two in step.

export type SyncStatusKind = 'synced' | 'pending' | 'stale'

export type Provider = 'garmin' | 'wahoo'

export interface Account {
  id: string
  provider: Provider
  rider: string
  label: string
  /** False while the provider adapter is still a stub. */
  implemented: boolean
  /** Whether the viewer may unlink this one — their own, or they're an admin. */
  mine: boolean
}

export interface LinkAccountRequest {
  provider: Provider
  label?: string
  /** Admins only: link on somebody else's behalf. */
  rider?: string
}

export interface SyncStatus {
  accountId: string
  status: SyncStatusKind
  remoteId?: string
  updatedAt?: string
}

export type Role = 'none' | 'viewer' | 'rider' | 'admin'

export type Permission =
  | 'routes:read'
  | 'routes:upload'
  | 'routes:edit-own'
  | 'routes:edit-any'
  | 'sync:push'
  | 'komoot:import'
  | 'garmin:sync'
  | 'accounts:manage'

export interface Me {
  /** Whether *this* request is signed in — not whether the deployment has
   *  auth turned on. Under mode oidc these can differ: an anonymous visitor
   *  reaches this endpoint too, so it has to say which one it means. */
  authenticated: boolean
  authMode: 'none' | 'proxy' | 'oidc'
  user?: string
  name?: string
  email?: string
  groups: string[]
  role: Role
  permissions: Permission[]
  /** Where signing out goes, for authMode proxy only — the identity
   *  provider's address, not ours, since the session belongs to the proxy
   *  and this app cannot end it. Absent when there is nothing to sign out of,
   *  and always absent under authMode oidc, which signs out through
   *  api.logout() instead: the app holds that session and ends it itself. */
  logoutUrl?: string
}

export interface KomootTour {
  id: string
  name: string
  sport: string
  distanceM: number
  ascentM: number
  changedAt?: string
  /** Already in the library — importing again would duplicate it. */
  imported: boolean
}

export interface KomootImportResult {
  imported: string[]
  skipped: Record<string, string>
}

/** One course already on the rider's own Garmin account — sync-back, the
 *  reverse direction from pushing. */
export interface GarminCourse {
  id: string
  name: string
  distanceM: number
  ascentM: number
  activityType: string
  createdAt?: string
  /** Already tracked as something this app pushed to this account — exact
   *  match, not a guess. */
  imported: boolean
  /** Set when a library route looks like the same ride by distance and
   *  start point. A hint, not a certainty — Garmin re-encodes track points
   *  its own way, so this is never an exact match the way `imported` is. */
  possibleDuplicate?: string
}

export interface GarminCourseImportResult {
  imported: string[]
  skipped: Record<string, string>
}

export interface AppConfig {
  /** Human-readable description of the route source. */
  source: string
  /**
   * Komoot import: "disabled" when nobody asked for it, "unconfigured" when
   * it is on but the credentials are missing, "ready" when it can be used.
   * The middle state is the one worth surfacing — it looks identical to
   * "disabled" unless the UI says otherwise.
   */
  komoot: 'disabled' | 'unconfigured' | 'ready'
}

export interface Route {
  slug: string
  /** Who uploaded it. Riders may only edit their own. */
  owner?: string
  name: string
  description: string
  tags: string[]
  distanceM: number
  ascentM: number
  startLat: number
  startLng: number
  pointCount: number
  contentHash: string
  origin: string
  updatedAt: string
  targets: string[]
  /** Targets naming accounts that do not exist — usually a typo. */
  unknownTargets: string[]
  syncState: SyncStatus[]
}

export interface UploadRequest {
  file: File
  name?: string
  description?: string
  tags?: string
  targets?: string
  uploadedBy?: string
}

export interface LibraryResponse {
  routes: Route[]
  problems: string[]
}

export interface PlanItem {
  op: 'create' | 'update' | 'delete'
  accountId: string
  slug: string
  reason: string
}

export interface PlanResponse {
  items: PlanItem[]
  inSync: number
  problems: string[]
}

export interface PushResponse {
  applied: number
  failures: string[]
  items: PlanItem[]
}

export interface TrackResponse {
  slug: string
  /** [lat, lon] pairs in track order. */
  points: [number, number][]
}

/**
 * The OAuth1 consumer Garmin sign-in is signed with.
 *
 * One pair for the whole deployment, not one per rider — it identifies the
 * app to Garmin. The value itself never reaches the browser.
 */
export interface GarminConsumer {
  configured: boolean
  /** Where the pair in use came from. */
  source?: 'settings' | 'environment'
  updatedBy?: string
  updatedAt?: string
  /** Whether this viewer may set it here — admin, with somewhere to keep it. */
  canManage: boolean
  /** Why they may not, when that is worth saying. */
  unavailable?: string
}

/** One rider's sign-in to their own Garmin Connect account. */
export interface GarminConnection {
  connected: boolean
  email?: string
  displayName?: string
  updatedAt?: string
  /** When the stored sign-in stops working — about a year after it was made. */
  expiresAt?: string
  expired?: boolean
  /** False when signing in could not be stored or completed; see `unavailable`. */
  canConnect: boolean
  /** Why signing in is not on offer, in words worth showing. */
  unavailable?: string
  /** Set when what is missing is the consumer, which an admin can supply. */
  consumer?: GarminConsumer
}

/** A head unit registered to a connected Garmin account.
 *
 *  Informational: a course is pushed to the account and Connect syncs it to
 *  whichever units can take it, so this is not a list to choose from — it is
 *  the answer to "will this reach my Edge?". */
export interface GarminDevice {
  id: string
  name: string
  /** When Connect last heard from the unit. Absent if it never has. */
  lastSync?: string
}

/** One rider's connection to their own Komoot account. */
export interface KomootConnection {
  connected: boolean
  email?: string
  displayName?: string
  updatedAt?: string
  /** True when the deployment supplies the account, so it cannot be unlinked here. */
  shared: boolean
  /** False when there is no encryption key: nothing could be stored, so nothing is offered. */
  canConnect: boolean
}
