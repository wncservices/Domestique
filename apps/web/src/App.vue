<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { Account, AppConfig, PlanResponse, Route } from '@/api/types'
import PlanPanel from '@/components/PlanPanel.vue'
import RouteCard from '@/components/RouteCard.vue'
import UploadPanel from '@/components/UploadPanel.vue'

const config = ref<AppConfig | null>(null)
const accounts = ref<Account[]>([])
const routes = ref<Route[]>([])
const problems = ref<string[]>([])
const plan = ref<PlanResponse | null>(null)
const failures = ref<string[]>([])

const loading = ref(true)
const pushing = ref(false)
const error = ref('')
const search = ref('')

const totalDistance = computed(
  () => routes.value.reduce((sum, r) => sum + r.distanceM, 0) / 1000,
)

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
    const [appConfig, accountList, library, currentPlan] = await Promise.all([
      api.config(),
      api.accounts(),
      api.routes(),
      api.plan(),
    ])
    config.value = appConfig
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
  <div class="app">
    <header class="masthead">
      <div>
        <h1>domestique</h1>
        <p class="tagline">Shared route library, carried to every head unit.</p>
        <p v-if="config" class="source">
          {{ config.source }}
          <span v-if="!config.writable"> · read-only, add routes by committing them</span>
        </p>
      </div>
      <dl class="totals">
        <div>
          <dt>Routes</dt>
          <dd>{{ routes.length }}</dd>
        </div>
        <div>
          <dt>Distance</dt>
          <dd>{{ totalDistance.toFixed(0) }} km</dd>
        </div>
        <div>
          <dt>Accounts</dt>
          <dd>{{ accounts.length }}</dd>
        </div>
      </dl>
    </header>

    <p v-if="error" class="error">Could not reach the API: {{ error }}</p>

    <ul v-if="problems.length" class="problems">
      <li v-for="problem in problems" :key="problem">{{ problem }}</li>
    </ul>

    <PlanPanel
      :plan="plan"
      :accounts="accounts"
      :pushing="pushing"
      :failures="failures"
      @push="push"
      @refresh="refresh"
    />

    <UploadPanel v-if="config?.writable" :accounts="accounts" @uploaded="refresh" />

    <section class="library">
      <header>
        <h2>Library</h2>
        <input v-model="search" type="search" placeholder="Filter by name, slug or tag" />
      </header>

      <p v-if="loading" class="muted">Loading routes…</p>
      <p v-else-if="!routes.length" class="muted">
        <template v-if="config?.writable">No routes yet — upload a GPX above.</template>
        <template v-else>
          No routes yet — commit a <code>route.gpx</code> to the routes repo and refresh.
        </template>
      </p>
      <p v-else-if="!visibleRoutes.length" class="muted">Nothing matches “{{ search }}”.</p>

      <div v-else class="grid">
        <RouteCard
          v-for="route in visibleRoutes"
          :key="route.slug"
          :route="route"
          :accounts="accounts"
          :writable="config?.writable ?? false"
          @deleted="refresh"
        />
      </div>
    </section>
  </div>
</template>

<style scoped>
.app {
  max-width: 1100px;
  margin: 0 auto;
  padding: 2rem 1.25rem 4rem;
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.masthead {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
  align-items: flex-end;
  justify-content: space-between;
}

h1 {
  margin: 0;
  font-size: 1.6rem;
  letter-spacing: -0.02em;
}

.tagline {
  margin: 0.25rem 0 0;
  color: var(--text-muted);
  font-size: 0.9rem;
}

.source {
  margin: 0.3rem 0 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.75rem;
  color: var(--text-muted);
}

.totals {
  display: flex;
  gap: 1.5rem;
  margin: 0;
}

.totals div {
  display: flex;
  flex-direction: column;
}

.totals dt {
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
}

.totals dd {
  margin: 0;
  font-size: 1.3rem;
  font-variant-numeric: tabular-nums;
}

.library header {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.9rem;
}

.library h2 {
  margin: 0;
  font-size: 1rem;
}

input[type='search'] {
  font: inherit;
  font-size: 0.85rem;
  padding: 0.4rem 0.7rem;
  min-width: 260px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1rem;
}

.muted {
  color: var(--text-muted);
  font-size: 0.9rem;
}

.error {
  margin: 0;
  padding: 0.7rem 0.9rem;
  border-radius: 8px;
  font-size: 0.88rem;
  background: color-mix(in srgb, var(--danger) 12%, transparent);
  color: var(--danger);
}

.problems {
  margin: 0;
  padding: 0.7rem 0.9rem 0.7rem 2rem;
  border-radius: 8px;
  font-size: 0.85rem;
  background: color-mix(in srgb, var(--warn) 12%, transparent);
  color: var(--warn);
}
</style>
