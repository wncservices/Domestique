import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { Account, AppConfig, Crew, Me, Permission, PlanResponse, Route } from '@/api/types'

/**
 * The state every page shares.
 *
 * Module-level rather than per-component: three pages want the same route
 * list, the same identity and the same set of head units, and fetching them
 * again on every navigation would make moving between tabs feel like a page
 * load. `refresh()` is what any page calls after changing something.
 */
const config = ref<AppConfig | null>(null)
const me = ref<Me | null>(null)
const accounts = ref<Account[]>([])
const routes = ref<Route[]>([])
const problems = ref<string[]>([])
const plan = ref<PlanResponse | null>(null)
const crews = ref<Crew[]>([])

const loading = ref(true)
const error = ref('')

/** Guards a second fetch while the first is still in flight. */
let inFlight: Promise<void> | null = null

async function load(): Promise<void> {
  // Every page's onMounted calls refresh() again, so this reruns on each
  // navigation, not just the first. Without resetting loading here, only
  // the very first call ever showed a loading state — every later fetch
  // ran invisibly, and a page whose own slice of data hadn't arrived yet
  // (e.g. the first visit to a page this session) rendered its empty state
  // before quietly swapping in real data a moment later.
  loading.value = true
  error.value = ''
  try {
    // config() and me() answer for anonymous visitors too (both exempted
    // server-side, api/server.go's authenticate) — every mode reaches this
    // page signed out at least once. The rest of the library is a rider's
    // own data, gated behind actually holding read access, so it is only
    // worth asking for once me() says so: fetching it without that would
    // just be a guaranteed 401, and bundling that into the same Promise.all
    // used to fail the whole load — including config and identity — over a
    // rejection that was completely expected.
    //
    // Checked via permissions, not identity.authenticated: that field means
    // "did this request go through a real login" (see its own doc comment
    // in types.ts), which is correctly false under mode: none — nobody logs
    // in, everyone is the local admin — but that is a different question
    // from "can this identity read the library." Gating on it here made
    // mode: none's own library page permanently empty.
    const [appConfig, identity] = await Promise.all([api.config(), api.me()])
    config.value = appConfig
    me.value = identity

    if (identity.permissions.includes('routes:read')) {
      const [accountList, library, currentPlan] = await Promise.all([
        api.accounts(),
        api.routes(),
        api.plan(),
      ])
      accounts.value = accountList
      routes.value = library.routes
      problems.value = library.problems
      plan.value = currentPlan
    } else {
      accounts.value = []
      routes.value = []
      problems.value = []
      plan.value = null
    }

    // A separate gate from routes:read: a viewer can read the library
    // without being able to manage crews (rider-level), so this cannot
    // ride along inside the block above.
    crews.value = identity.permissions.includes('crews:manage') ? await api.crews() : []
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

function refresh(): Promise<void> {
  if (!inFlight) {
    inFlight = load().finally(() => {
      inFlight = null
    })
  }
  return inFlight
}

/** Drives what the UI offers. Showing a control the API will refuse is worse
 *  than hiding it: the rider learns the rules by being told no. */
function can(permission: Permission): boolean {
  return me.value?.permissions.includes(permission) ?? false
}

const canUpload = computed(() => can('routes:upload'))
const canImportKomoot = computed(() => can('komoot:import'))
const canSyncGarmin = computed(() => can('garmin:sync'))
const canSyncWahoo = computed(() => can('wahoo:sync'))
const canPush = computed(() => can('sync:push'))
const canManageAccounts = computed(() => can('accounts:manage'))
const canManagePeople = computed(() => can('people:manage'))
const canManageCrews = computed(() => can('crews:manage'))
const canManageSettings = computed(() => can('settings:manage'))

const totalDistance = computed(() => routes.value.reduce((sum, r) => sum + r.distanceM, 0) / 1000)
const totalAscent = computed(() => routes.value.reduce((sum, r) => sum + r.ascentM, 0))

/** Komoot is on the Add page, but only when the deployment has it enabled. */
const komootEnabled = computed(() => (config.value?.komoot ?? 'disabled') !== 'disabled')

export function useLibrary() {
  return {
    config,
    me,
    accounts,
    routes,
    problems,
    plan,
    crews,
    loading,
    error,
    refresh,
    can,
    canUpload,
    canImportKomoot,
    canSyncGarmin,
    canSyncWahoo,
    canPush,
    canManageAccounts,
    canManagePeople,
    canManageCrews,
    canManageSettings,
    komootEnabled,
    totalDistance,
    totalAscent,
  }
}
