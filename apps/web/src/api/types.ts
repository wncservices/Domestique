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

export interface Route {
  slug: string
  name: string
  description: string
  tags: string[]
  distanceM: number
  ascentM: number
  startLat: number
  startLng: number
  pointCount: number
  contentHash: string
  targets: string[]
  syncState: SyncStatus[]
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
