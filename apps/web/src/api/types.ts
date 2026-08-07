// Mirrors the DTOs in apps/api/internal/api/server.go. Keep the two in step.

export type SyncStatusKind = 'synced' | 'pending' | 'stale'

export interface Account {
  id: string
  provider: 'garmin' | 'wahoo'
  rider: string
  label: string
  /** False while the provider adapter is still a stub (Phases 3 and 4). */
  implemented: boolean
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
  /** True when the source accepts uploads; false for a git-backed directory. */
  writable: boolean
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
