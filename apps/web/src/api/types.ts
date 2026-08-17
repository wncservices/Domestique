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
  /** Other riders with an account carrying the same provider and label —
   *  usually the same real device account, linked twice under a rider
   *  identity this deployment had not yet recognised as the same person. A
   *  hint, not a certainty. */
  possibleDuplicateOf?: string[]
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
  | 'people:manage'
  | 'crews:manage'

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

/** One rider's standing with a crew. */
export interface CrewMember {
  rider: string
  status: 'pending' | 'approved'
}

/** A set of riders who trust each other with their routes — the only way a
 *  route may reach an account beyond its own owner's. See RouteCard.vue's
 *  target picker and CrewsPage.vue. */
export interface Crew {
  id: string
  name: string
  owner: string
  /** Whether the caller may manage this crew — its owner, or an admin.
   *  Members is only ever present when this is true. */
  mine: boolean
  /** The caller's own standing with this crew — always present, even for a
   *  crew that isn't theirs, so the UI knows whether to offer "Request to
   *  join" or show "Pending". */
  membershipStatus: 'none' | 'pending' | 'approved'
  /** The approved roster size. Always visible — a rider needs it to judge
   *  whether a crew is worth requesting to join. */
  memberCount: number
  /** Pending and approved members together. Only present when `mine`. */
  members?: CrewMember[]
}

export interface CreateCrewRequest {
  name: string
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

/** Garmin courses that look like repeated copies of each other — same name,
 *  same distance — found by comparing the account's own course list against
 *  itself, not against the library. */
export interface GarminDuplicateGroup {
  name: string
  courses: GarminCourse[]
}

/** The three roles this page actually offers a choice between — Role also
 *  has 'none', which is not something anyone is invited or set as. */
export type AssignableRole = 'admin' | 'rider' | 'viewer'

/** Someone with access to this deployment — the admin People page. Always
 *  holds at least the gate role, so role here is never 'none' the way the
 *  broader Role type otherwise allows. */
export interface Person {
  id: string
  email: string
  name: string
  /** The role this person actually resolves to — the same computation
   *  Identify runs at sign-in, not just whatever Auth0 roles are assigned. */
  role: AssignableRole
  createdAt?: string
  lastLogin?: string
}

export interface InvitePersonRequest {
  email: string
  name?: string
  role: AssignableRole
}

export interface AppConfig {
  /** Human-readable description of the route source — database host and
   *  port included, so the server only sends this to an admin. Absent for
   *  everyone else, the same way GarminConnection's consumer field is. */
  source?: string
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
  /** Crew ids this route is shared to — own devices are implicit and never
   *  listed here. Empty/absent means the owner's own accounts only. */
  targets: string[]
  /** Entries in `targets` that no longer resolve — a crew since deleted,
   *  one the owner left, or (from before crews existed) a raw account id.
   *  Never syncs anywhere either way. */
  unknownTargets: string[]
  /** Crews the route's *owner* currently, approvedly, belongs to — what a
   *  target picker may legally offer, correct even when an admin is
   *  editing someone else's route. */
  ownerCrews: { id: string; name: string }[]
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

/** Library routes that look like repeated imports of the same real ride —
 *  found by comparing the library against itself (same name, similar
 *  distance), not against any one provider's own account. */
export interface RouteDuplicateGroup {
  name: string
  routes: Route[]
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

/** One rider's authorization of their own Wahoo account.
 *
 *  Unlike Garmin/Komoot there is no sign-in form here — connecting is a
 *  redirect to Wahoo's own consent screen (`/wahoo/connect`), so this
 *  carries no email/password fields to fill in, only status to show. */
export interface WahooConnection {
  connected: boolean
  email?: string
  displayName?: string
  updatedAt?: string
  /** When the stored access token stops working — unlike Garmin's rough
   *  one-year guess, this is exact, and expiring it does not mean
   *  reconnecting: a refresh happens automatically at the next push. */
  expiresAt?: string
  expired?: boolean
  /** False when a connection could not be stored or completed; see `unavailable`. */
  canConnect: boolean
  /** Why connecting is not on offer, in words worth showing. */
  unavailable?: string
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
