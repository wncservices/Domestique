import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { Account, AppConfig, Me, Permission, PlanResponse, Route } from '@/api/types'

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

const loading = ref(true)
const error = ref('')

/** Guards a second fetch while the first is still in flight. */
let inFlight: Promise<void> | null = null

async function load(): Promise<void> {
  error.value = ''
  try {
    // config() and me() answer for anonymous visitors too (both exempted
    // server-side, api/server.go's authenticate) — every mode reaches this
    // page signed out at least once. The rest of the library is a rider's
    // own data, gated behind actually being signed in, so it is only worth
    // asking for once me() says so: fetching it anonymously would just be a
    // guaranteed 401, and bundling that into the same Promise.all used to
    // fail the whole load — including config and identity — over a
    // rejection that was completely expected.
    const [appConfig, identity] = await Promise.all([api.config(), api.me()])
    config.value = appConfig
    me.value = identity

    if (identity.authenticated) {
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
const canPush = computed(() => can('sync:push'))
const canManageAccounts = computed(() => can('accounts:manage'))

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
    loading,
    error,
    refresh,
    can,
    canUpload,
    canImportKomoot,
    canSyncGarmin,
    canPush,
    canManageAccounts,
    komootEnabled,
    totalDistance,
    totalAscent,
  }
}
