<script setup lang="ts">
import { computed } from 'vue'
import type { Account, SyncStatus } from '@/api/types'

const props = defineProps<{ status: SyncStatus; account?: Account }>()

const label = computed(() => props.account?.label || props.status.accountId)

const title = computed(() => {
  const when = props.status.updatedAt ? ` (last push ${props.status.updatedAt})` : ''
  switch (props.status.status) {
    case 'synced':
      return `On ${label.value}${when}`
    case 'stale':
      return `Changed since the last push to ${label.value}${when}`
    default:
      return `Not pushed to ${label.value} yet`
  }
})
</script>

<template>
  <span class="badge" :class="status.status" :title="title">
    <span class="dot" aria-hidden="true" />
    {{ label }}
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.15rem 0.55rem;
  border-radius: 999px;
  font-size: 0.78rem;
  border: 1px solid var(--border);
  background: var(--surface-sunken);
  color: var(--text-muted);
  white-space: nowrap;
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
}

.synced {
  color: var(--ok);
  border-color: color-mix(in srgb, var(--ok) 35%, transparent);
}

.stale {
  color: var(--warn);
  border-color: color-mix(in srgb, var(--warn) 35%, transparent);
}

.pending {
  color: var(--text-muted);
}
</style>
