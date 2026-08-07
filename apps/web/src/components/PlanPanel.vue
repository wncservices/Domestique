<script setup lang="ts">
import { computed } from 'vue'
import type { Account, PlanResponse } from '@/api/types'

const props = defineProps<{
  plan: PlanResponse | null
  accounts: Account[]
  pushing: boolean
  failures: string[]
  canPush?: boolean
}>()

const emit = defineEmits<{ push: []; refresh: [] }>()

const changes = computed(() => props.plan?.items ?? [])

/**
 * Every provider adapter is still a stub, so a push would fail on every item.
 * Say that up front instead of letting the button produce a wall of errors.
 */
const noAdapters = computed(
  () => props.accounts.length > 0 && props.accounts.every((a) => !a.implemented),
)
const notAllowed = computed(() => props.canPush === false)
const blocked = computed(() => noAdapters.value || notAllowed.value)

const blockedReason = computed(() => {
  if (notAllowed.value) return 'Your role does not allow pushing to the head units.'
  if (noAdapters.value) {
    return 'No provider adapter is wired up yet — pushes will fail until Phase 3 (Garmin) or Phase 4 (Wahoo) lands.'
  }
  return ''
})
</script>

<template>
  <section class="panel">
    <header>
      <div>
        <h2>Pending changes</h2>
        <p v-if="plan" class="summary">
          {{ changes.length }} change{{ changes.length === 1 ? '' : 's' }},
          {{ plan.inSync }} already in sync
        </p>
      </div>
      <div class="actions">
        <button type="button" class="ghost" @click="emit('refresh')">Refresh</button>
        <button
          type="button"
          class="primary"
          :disabled="pushing || !changes.length || blocked"
          :title="blockedReason"
          @click="emit('push')"
        >
          {{ pushing ? 'Pushing…' : 'Push to devices' }}
        </button>
      </div>
    </header>

    <p v-if="blocked" class="notice">{{ blockedReason }}</p>

    <table v-if="changes.length">
      <thead>
        <tr>
          <th>Op</th>
          <th>Account</th>
          <th>Route</th>
          <th>Reason</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in changes" :key="`${item.accountId}:${item.slug}`">
          <td><span class="op" :class="item.op">{{ item.op }}</span></td>
          <td class="mono">{{ item.accountId }}</td>
          <td class="mono">{{ item.slug }}</td>
          <td class="reason">{{ item.reason }}</td>
        </tr>
      </tbody>
    </table>
    <p v-else-if="plan" class="empty">Everything is where it should be.</p>

    <ul v-if="failures.length" class="failures">
      <li v-for="failure in failures" :key="failure">{{ failure }}</li>
    </ul>

    <ul v-if="plan?.problems.length" class="failures">
      <li v-for="problem in plan.problems" :key="problem">{{ problem }}</li>
    </ul>
  </section>
</template>

<style scoped>
.panel {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  padding: 1rem 1.15rem 1.15rem;
}

header {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: center;
  justify-content: space-between;
}

h2 {
  margin: 0;
  font-size: 1rem;
}

.summary {
  margin: 0.2rem 0 0;
  font-size: 0.85rem;
  color: var(--text-muted);
}

.actions {
  display: flex;
  gap: 0.5rem;
}

button {
  font: inherit;
  font-size: 0.85rem;
  padding: 0.4rem 0.85rem;
  border-radius: 8px;
  border: 1px solid var(--border);
  cursor: pointer;
  background: var(--surface-sunken);
  color: var(--text);
}

button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

button.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--on-accent);
}

.notice {
  margin: 0.85rem 0 0;
  padding: 0.6rem 0.75rem;
  border-radius: 8px;
  font-size: 0.85rem;
  background: color-mix(in srgb, var(--warn) 12%, transparent);
  color: var(--warn);
}

table {
  width: 100%;
  margin-top: 0.9rem;
  border-collapse: collapse;
  font-size: 0.85rem;
}

th {
  text-align: left;
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
  padding-bottom: 0.35rem;
}

td {
  padding: 0.4rem 0.6rem 0.4rem 0;
  border-top: 1px solid var(--border);
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8rem;
}

.reason {
  color: var(--text-muted);
}

.op {
  text-transform: uppercase;
  font-size: 0.7rem;
  letter-spacing: 0.05em;
  padding: 0.1rem 0.4rem;
  border-radius: 4px;
  background: var(--surface-sunken);
}

.op.create {
  color: var(--ok);
}

.op.update {
  color: var(--warn);
}

.op.delete {
  color: var(--danger);
}

.empty {
  margin: 0.9rem 0 0;
  font-size: 0.88rem;
  color: var(--text-muted);
}

.failures {
  margin: 0.9rem 0 0;
  padding-left: 1.1rem;
  font-size: 0.83rem;
  color: var(--danger);
}
</style>
