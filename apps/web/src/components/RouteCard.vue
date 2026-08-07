<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { Account, Me, Route } from '@/api/types'
import SyncBadge from './SyncBadge.vue'
import TrackPreview from './TrackPreview.vue'

const props = defineProps<{
  route: Route
  accounts: Account[]
  writable: boolean
  me?: Me | null
}>()
const emit = defineEmits<{ deleted: [] }>()

const distance = computed(() => `${(props.route.distanceM / 1000).toFixed(1)} km`)
const ascent = computed(() => `${Math.round(props.route.ascentM)} m`)
const gpxUrl = computed(() => api.gpxUrl(props.route.slug))

const deleting = ref(false)
const deleteError = ref('')

/** Riders may only remove what they uploaded; admins anything. Mirrors the
 *  server's rule so the button is absent rather than rejected. */
const canDelete = computed(() => {
  if (!props.writable) return false
  const me = props.me
  if (!me) return false
  if (me.permissions.includes('routes:edit-any')) return true
  if (!me.permissions.includes('routes:edit-own')) return false
  return !props.route.owner || props.route.owner.toLowerCase() === (me.user ?? '').toLowerCase()
})

function accountFor(id: string): Account | undefined {
  return props.accounts.find((a) => a.id === id)
}

async function remove() {
  // Deleting here also queues a delete on every device that holds it, so make
  // sure that is what the rider meant.
  if (!confirm(`Delete “${props.route.name}”? It will be removed from the devices too.`)) {
    return
  }
  deleting.value = true
  deleteError.value = ''
  try {
    await api.remove(props.route.slug)
    emit('deleted')
  } catch (err) {
    deleteError.value = err instanceof Error ? err.message : String(err)
  } finally {
    deleting.value = false
  }
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

    <p v-if="route.unknownTargets.length" class="unknown">
      unknown target{{ route.unknownTargets.length === 1 ? '' : 's' }}:
      {{ route.unknownTargets.join(', ') }}
    </p>

    <footer>
      <SyncBadge
        v-for="status in route.syncState"
        :key="status.accountId"
        :status="status"
        :account="accountFor(status.accountId)"
      />
      <span v-if="!route.syncState.length" class="untargeted">no targets</span>
      <span v-if="route.owner" class="owner">{{ route.owner }}</span>

      <span class="spacer" />
      <a class="action" :href="gpxUrl" download>GPX</a>
      <button
        v-if="canDelete"
        type="button"
        class="action danger"
        :disabled="deleting"
        @click="remove"
      >
        {{ deleting ? 'Deleting…' : 'Delete' }}
      </button>
    </footer>

    <p v-if="deleteError" class="error">{{ deleteError }}</p>
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

.owner {
  font-size: 0.72rem;
  color: var(--text-muted);
}

.spacer {
  flex: 1;
}

.action {
  font: inherit;
  font-size: 0.75rem;
  padding: 0.15rem 0.5rem;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: var(--surface-sunken);
  color: var(--text-muted);
  cursor: pointer;
  text-decoration: none;
}

.action:hover {
  color: var(--text);
}

.action.danger:hover {
  color: var(--danger);
  border-color: color-mix(in srgb, var(--danger) 40%, transparent);
}

.action:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.unknown {
  margin: 0;
  font-size: 0.78rem;
  color: var(--warn);
}

.error {
  margin: 0.4rem 0 0;
  font-size: 0.78rem;
  color: var(--danger);
}
</style>
