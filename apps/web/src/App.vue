<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { Account, AppConfig, Me, Permission, PlanResponse, Route } from '@/api/types'
import AccountsPanel from '@/components/AccountsPanel.vue'
import ColorModeToggle from '@/components/ColorModeToggle.vue'
import KomootPanel from '@/components/KomootPanel.vue'
import PlanPanel from '@/components/PlanPanel.vue'
import RouteCard from '@/components/RouteCard.vue'
import UploadPanel from '@/components/UploadPanel.vue'

const config = ref<AppConfig | null>(null)
const me = ref<Me | null>(null)
const accounts = ref<Account[]>([])
const routes = ref<Route[]>([])
const problems = ref<string[]>([])
const plan = ref<PlanResponse | null>(null)
const failures = ref<string[]>([])

const loading = ref(true)
const pushing = ref(false)
const error = ref('')
const search = ref('')

/** Drives what the UI offers. Showing a control the API will refuse is worse
 *  than hiding it: the rider learns the rules by being told no. */
function can(permission: Permission): boolean {
  return me.value?.permissions.includes(permission) ?? false
}

const canUpload = computed(() => can('routes:upload'))
const canImportKomoot = computed(() => can('komoot:import'))
const canPush = computed(() => can('sync:push'))
const canManageAccounts = computed(() => can('accounts:manage'))

const roleColor = computed(() => {
  switch (me.value?.role) {
    case 'admin':
      return 'primary' as const
    case 'rider':
      return 'success' as const
    default:
      return 'neutral' as const
  }
})

const totalDistance = computed(() => routes.value.reduce((sum, r) => sum + r.distanceM, 0) / 1000)
const totalAscent = computed(() => routes.value.reduce((sum, r) => sum + r.ascentM, 0))

const stats = computed(() => [
  { label: 'Routes', value: String(routes.value.length), icon: 'i-lucide-route' },
  { label: 'Distance', value: `${totalDistance.value.toFixed(0)} km`, icon: 'i-lucide-ruler' },
  { label: 'Ascent', value: `${Math.round(totalAscent.value).toLocaleString()} m`, icon: 'i-lucide-mountain' },
  { label: 'Head units', value: String(accounts.value.length), icon: 'i-lucide-watch' },
])

const visibleRoutes = computed(() => {
  const needle = search.value.trim().toLowerCase()
  if (!needle) return routes.value
  return routes.value.filter(
    (route) =>
      route.name.toLowerCase().includes(needle) ||
      route.slug.toLowerCase().includes(needle) ||
      route.tags.some((tag) => tag.toLowerCase().includes(needle)),
  )
})

async function refresh() {
  error.value = ''
  try {
    const [appConfig, identity, accountList, library, currentPlan] = await Promise.all([
      api.config(),
      api.me(),
      api.accounts(),
      api.routes(),
      api.plan(),
    ])
    config.value = appConfig
    me.value = identity
    accounts.value = accountList
    routes.value = library.routes
    problems.value = library.problems
    plan.value = currentPlan
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function push() {
  pushing.value = true
  failures.value = []
  try {
    const result = await api.push()
    failures.value = result.failures
    await refresh()
  } catch (err) {
    failures.value = [err instanceof Error ? err.message : String(err)]
  } finally {
    pushing.value = false
  }
}

onMounted(refresh)
</script>

<template>
  <UApp>
    <!-- Sticky, because the library scrolls and the identity badge is the
         thing you want to be sure of before you push anything. -->
    <div class="app-header sticky top-0 z-20">
      <UContainer class="flex max-w-5xl items-center justify-between gap-4 py-3">
        <div class="flex min-w-0 items-center gap-3">
          <span
            class="flex size-9 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary"
            aria-hidden="true"
          >
            <UIcon name="i-lucide-bike" class="size-5" />
          </span>
          <div class="min-w-0">
            <h1 class="truncate text-base font-semibold tracking-tight text-highlighted">
              Domestique
            </h1>
            <p class="truncate text-xs text-muted">
              Shared route library, carried to every head unit.
            </p>
          </div>
        </div>

        <div class="flex shrink-0 items-center gap-2">
          <UBadge
            v-if="me?.authenticated"
            :color="roleColor"
            variant="subtle"
            icon="i-lucide-user"
            class="hidden sm:inline-flex"
          >
            {{ me.name || me.user }} · {{ me.role }}
          </UBadge>
          <UTooltip
            v-else-if="me"
            text="Anyone who can reach this page has full access. Put it behind Authelia before exposing it."
          >
            <UBadge color="warning" variant="subtle" icon="i-lucide-shield-off">
              <span class="hidden sm:inline">no login required</span>
              <span class="sm:hidden">open</span>
            </UBadge>
          </UTooltip>

          <ColorModeToggle />
        </div>
      </UContainer>
    </div>

    <UContainer class="flex max-w-5xl flex-col gap-6 py-8">
      <section class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div v-for="stat in stats" :key="stat.label" class="app-card px-4 py-3">
          <div class="flex items-center gap-1.5 text-[0.7rem] uppercase tracking-wide text-dimmed">
            <UIcon :name="stat.icon" class="size-3.5" />
            {{ stat.label }}
          </div>
          <div class="mt-1 truncate text-2xl tabular-nums text-highlighted">
            {{ stat.value }}
          </div>
        </div>
      </section>

      <UAlert
        v-if="error"
        color="error"
        variant="subtle"
        icon="i-lucide-plug-zap"
        title="Could not reach the API"
        :description="error"
      />

      <UAlert
        v-for="problem in problems"
        :key="problem"
        color="warning"
        variant="subtle"
        icon="i-lucide-file-warning"
        :description="problem"
      />

      <PlanPanel
        :plan="plan"
        :accounts="accounts"
        :pushing="pushing"
        :failures="failures"
        :can-push="canPush"
        @push="push"
        @refresh="refresh"
      />

      <AccountsPanel
        :accounts="accounts"
        :me="me"
        :can-manage="canManageAccounts"
        @changed="refresh"
      />

      <UploadPanel v-if="canUpload" :accounts="accounts" :me="me" @uploaded="refresh" />

      <KomootPanel
        v-if="canImportKomoot && config && config.komoot !== 'disabled'"
        :state="config.komoot"
        @imported="refresh"
      />

      <section>
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h2 class="font-medium text-highlighted">Library</h2>
          <UInput
            v-model="search"
            icon="i-lucide-search"
            placeholder="Filter by name, slug or tag"
            class="w-full sm:w-72"
          />
        </div>

        <div v-if="loading" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <USkeleton v-for="n in 3" :key="n" class="h-72" />
        </div>

        <UEmpty
          v-else-if="!routes.length"
          icon="i-lucide-route"
          title="No routes yet"
          description="Upload a GPX above, or import from Komoot."
        />

        <UEmpty
          v-else-if="!visibleRoutes.length"
          icon="i-lucide-search-x"
          title="Nothing matches"
          :description="`No route matches “${search}”.`"
        />

        <div v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <RouteCard
            v-for="route in visibleRoutes"
            :key="route.slug"
            :route="route"
            :accounts="accounts"
            :writable="canUpload"
            :me="me"
            @deleted="refresh"
          />
        </div>
      </section>
      <footer
        v-if="config"
        class="flex flex-wrap items-center justify-between gap-2 border-t border-default pt-4 text-xs text-dimmed"
      >
        <span class="font-mono">{{ config.source }}</span>
        <span>Domestique</span>
      </footer>
    </UContainer>
  </UApp>
</template>
