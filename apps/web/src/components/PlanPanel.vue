<script setup lang="ts">
import { computed, h, resolveComponent } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import type { Account, PlanItem, PlanResponse } from '@/api/types'

const props = defineProps<{
  plan: PlanResponse | null
  accounts: Account[]
  pushing: boolean
  failures: string[]
  canPush?: boolean
}>()

const emit = defineEmits<{ push: []; refresh: [] }>()

const UBadge = resolveComponent('UBadge')

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
    return 'No provider adapter is wired up yet — pushes will fail until the Garmin and Wahoo adapters land.'
  }
  return ''
})

const opColor = { create: 'success', update: 'warning', delete: 'error' } as const

const columns: TableColumn<PlanItem>[] = [
  {
    accessorKey: 'op',
    header: 'Op',
    cell: ({ row }) =>
      h(
        UBadge,
        {
          color: opColor[row.original.op] ?? 'neutral',
          variant: 'subtle',
          size: 'sm',
        },
        () => row.original.op,
      ),
  },
  { accessorKey: 'accountId', header: 'Account' },
  { accessorKey: 'slug', header: 'Route' },
  { accessorKey: 'reason', header: 'Reason' },
]
</script>

<template>
  <UCard variant="outline">
    <template #header>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 class="font-medium text-highlighted">Pending changes</h2>
          <p v-if="plan" class="text-sm text-muted">
            {{ changes.length }} change{{ changes.length === 1 ? '' : 's' }},
            {{ plan.inSync }} already in sync
          </p>
        </div>
        <div class="flex gap-2">
          <UButton
            icon="i-lucide-refresh-cw"
            color="neutral"
            variant="ghost"
            @click="emit('refresh')"
          >
            Refresh
          </UButton>
          <UTooltip :text="blockedReason" :disabled="!blocked">
            <UButton
              icon="i-lucide-upload-cloud"
              :loading="pushing"
              :disabled="pushing || !changes.length || blocked"
              @click="emit('push')"
            >
              Push to devices
            </UButton>
          </UTooltip>
        </div>
      </div>
    </template>

    <UAlert
      v-if="blocked"
      color="warning"
      variant="subtle"
      icon="i-lucide-info"
      :description="blockedReason"
      class="mb-4"
    />

    <UTable
      v-if="changes.length"
      :data="changes"
      :columns="columns"
      :ui="{ td: 'text-sm', th: 'text-xs uppercase tracking-wide' }"
    />
    <p v-else-if="plan" class="text-sm text-muted">Everything is where it should be.</p>

    <UAlert
      v-for="failure in failures"
      :key="failure"
      color="error"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :description="failure"
      class="mt-3"
    />

    <UAlert
      v-for="problem in plan?.problems ?? []"
      :key="problem"
      color="warning"
      variant="subtle"
      icon="i-lucide-file-warning"
      :description="problem"
      class="mt-3"
    />
  </UCard>
</template>
