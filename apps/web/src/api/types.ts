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
  | 'accounts:manage'

export interface Me {
  /** False when the app runs without authentication (everyone is admin). */
  authenticated: boolean
  authMode: 'none' | 'proxy'
  user?: string
  name?: string
  email?: string
  groups: string[]
  role: Role
  permissions: Permission[]
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
