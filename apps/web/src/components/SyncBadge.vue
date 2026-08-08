<script setup lang="ts">
import { computed } from 'vue'
import type { Account, SyncStatus } from '@/api/types'

const props = defineProps<{ status: SyncStatus; account?: Account }>()

const label = computed(() => props.account?.label || props.status.accountId)

const appearance = computed(() => {
  switch (props.status.status) {
    case 'synced':
      return { color: 'success' as const, icon: 'i-lucide-check' }
    case 'stale':
      return { color: 'warning' as const, icon: 'i-lucide-refresh-cw' }
    default:
      return { color: 'neutral' as const, icon: 'i-lucide-clock' }
  }
})

const tooltip = computed(() => {
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
  <UTooltip :text="tooltip">
    <UBadge
      :color="appearance.color"
      :icon="appearance.icon"
      variant="subtle"
      size="sm"
      class="cursor-default"
    >
      {{ label }}
    </UBadge>
  </UTooltip>
</template>
