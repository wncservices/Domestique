<script setup lang="ts">
import { computed } from 'vue'
import type { Account, Route } from '@/api/types'
import SyncBadge from './SyncBadge.vue'
import TrackPreview from './TrackPreview.vue'

const props = defineProps<{ route: Route; accounts: Account[] }>()

const distance = computed(() => `${(props.route.distanceM / 1000).toFixed(1)} km`)
const ascent = computed(() => `${Math.round(props.route.ascentM)} m`)

function accountFor(id: string): Account | undefined {
  return props.accounts.find((a) => a.id === id)
}
</script>

<template>
  <article class="card">
    <TrackPreview :slug="route.slug" />

    <header>
      <h3>{{ route.name }}</h3>
      <p class="slug">{{ route.slug }}</p>
    </header>

    <p v-if="route.description" class="description">{{ route.description }}</p>

    <dl class="stats">
      <div>
        <dt>Distance</dt>
        <dd>{{ distance }}</dd>
      </div>
      <div>
        <dt>Ascent</dt>
        <dd>{{ ascent }}</dd>
      </div>
      <div>
        <dt>Points</dt>
        <dd>{{ route.pointCount.toLocaleString() }}</dd>
      </div>
    </dl>

    <ul v-if="route.tags.length" class="tags">
      <li v-for="tag in route.tags" :key="tag">{{ tag }}</li>
    </ul>

    <footer>
      <SyncBadge
        v-for="status in route.syncState"
        :key="status.accountId"
        :status="status"
        :account="accountFor(status.accountId)"
      />
      <span v-if="!route.syncState.length" class="untargeted">no targets</span>
    </footer>
  </article>
</template>

<style scoped>
.card {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 1rem;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
}

header h3 {
  margin: 0;
  font-size: 1.05rem;
}

.slug {
  margin: 0.15rem 0 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.75rem;
  color: var(--text-muted);
}

.description {
  margin: 0;
  font-size: 0.9rem;
  color: var(--text-muted);
}

.stats {
  display: flex;
  gap: 1.25rem;
  margin: 0;
}

.stats div {
  display: flex;
  flex-direction: column;
}

.stats dt {
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
}

.stats dd {
  margin: 0;
  font-size: 1rem;
  font-variant-numeric: tabular-nums;
}

.tags {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  margin: 0;
  padding: 0;
  list-style: none;
}

.tags li {
  font-size: 0.72rem;
  padding: 0.1rem 0.45rem;
  border-radius: 4px;
  background: var(--surface-sunken);
  color: var(--text-muted);
}

footer {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
  margin-top: auto;
  padding-top: 0.25rem;
}

.untargeted {
  font-size: 0.78rem;
  color: var(--text-muted);
}
</style>
