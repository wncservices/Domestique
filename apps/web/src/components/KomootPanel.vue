<script setup lang="ts">
import { computed, h, onMounted, ref, resolveComponent } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { useToast } from '@nuxt/ui/composables'
import { api } from '@/api/client'
import type { KomootTour } from '@/api/types'

const props = defineProps<{ state: 'unconfigured' | 'ready' }>()
const emit = defineEmits<{ imported: [] }>()

const toast = useToast()
const UBadge = resolveComponent('UBadge')
const UCheckbox = resolveComponent('UCheckbox')

const tours = ref<KomootTour[]>([])
const selected = ref<string[]>([])
const loading = ref(true)
const importing = ref(false)
const error = ref('')
/** Whether the panel can actually do anything, as opposed to being shown. */
const ready = computed(() => props.state === 'ready')

const importable = computed(() => tours.value.filter((t) => !t.imported))
const canImport = computed(() => selected.value.length > 0 && !importing.value)
const allSelected = computed(
  () => importable.value.length > 0 && selected.value.length === importable.value.length,
)

function toggle(id: string, on: boolean) {
  selected.value = on ? [...selected.value, id] : selected.value.filter((s) => s !== id)
}

function toggleAll(on: boolean) {
  selected.value = on ? importable.value.map((t) => t.id) : []
}

const columns: TableColumn<KomootTour>[] = [
  {
    id: 'select',
    header: () =>
      h(UCheckbox, {
        modelValue: allSelected.value,
        disabled: importable.value.length === 0,
        'onUpdate:modelValue': (v: boolean | 'indeterminate') => toggleAll(v === true),
        'aria-label': 'Select all importable tours',
      }),
    cell: ({ row }) =>
      h(UCheckbox, {
        modelValue: selected.value.includes(row.original.id),
        disabled: row.original.imported,
        'onUpdate:modelValue': (v: boolean | 'indeterminate') => toggle(row.original.id, v === true),
        'aria-label': `Select ${row.original.name}`,
      }),
  },
  { accessorKey: 'name', header: 'Tour' },
  {
    accessorKey: 'distanceM',
    header: 'Distance',
    cell: ({ row }) => `${(row.original.distanceM / 1000).toFixed(1)} km`,
  },
  {
    accessorKey: 'ascentM',
    header: 'Ascent',
    cell: ({ row }) => `${Math.round(row.original.ascentM)} m`,
  },
  { accessorKey: 'sport', header: 'Sport' },
  {
    id: 'status',
    header: '',
    cell: ({ row }) =>
      row.original.imported
        ? h(UBadge, { color: 'success', variant: 'subtle', size: 'sm' }, () => 'imported')
        : null,
  },
]

async function load() {
  loading.value = true
  error.value = ''
  try {
    tours.value = await api.komootTours()
  } catch (err) {
    // Availability now comes from /api/config, so anything reaching here is a
    // real failure rather than the feature being switched off.
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function runImport() {
  importing.value = true
  error.value = ''
  try {
    const result = await api.komootImport(selected.value)
    const skipped = Object.entries(result.skipped)

    toast.add({
      title: `Imported ${result.imported.length} route${result.imported.length === 1 ? '' : 's'}`,
      description: skipped.length ? `${skipped.length} skipped.` : undefined,
      icon: 'i-lucide-download',
      color: result.imported.length ? 'success' : 'warning',
    })
    // Say *why* each was skipped; "skipped 3" alone is useless.
    if (skipped.length) {
      error.value = skipped.map(([id, reason]) => `${id}: ${reason}`).join(' · ')
    }

    selected.value = []
    await load()
    emit('imported')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    importing.value = false
  }
}

onMounted(() => {
  if (ready.value) load()
  else loading.value = false
})
</script>

<template>
  <UCard variant="outline">
    <template #header>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 class="flex items-center gap-2 font-medium text-highlighted">
            <UIcon name="i-lucide-mountain-snow" />
            Import from Komoot
          </h2>
          <p class="text-sm text-muted">
            <template v-if="!ready">Enabled, but not signed in</template>
            <template v-else-if="loading">Loading tours…</template>
            <template v-else-if="!tours.length">No planned routes in that account.</template>
            <template v-else>
              {{ importable.length }} of {{ tours.length }} not imported yet
            </template>
          </p>
        </div>
        <div class="flex gap-2">
          <UButton
            icon="i-lucide-refresh-cw"
            color="neutral"
            variant="ghost"
            :loading="loading"
            :disabled="!ready"
            @click="load"
          >
            Refresh
          </UButton>
          <UButton
            icon="i-lucide-download"
            :loading="importing"
            :disabled="!canImport"
            @click="runImport"
          >
            Import{{ selected.length ? ` ${selected.length}` : '' }}
          </UButton>
        </div>
      </div>
    </template>

    <UAlert
      v-if="error"
      color="error"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :description="error"
      class="mb-4"
    />

    <!-- Enabled but not signed in. Saying so beats hiding the panel: the
         feature was deliberately switched on, and the gap is two env vars. -->
    <UAlert
      v-if="!ready"
      color="neutral"
      variant="subtle"
      icon="i-lucide-key-round"
      title="Komoot is enabled but not signed in"
      description="Set KOMOOT_EMAIL and KOMOOT_PASSWORD in the environment and restart. Running locally, put them in a .env file next to compose.yaml."
    />

    <UTable
      v-else-if="tours.length || loading"
      :data="tours"
      :columns="columns"
      :loading="loading"
      :ui="{ td: 'text-sm' }"
    />
    <UEmpty
      v-else
      icon="i-lucide-mountain-snow"
      title="Nothing to import"
      description="Plan a route in Komoot and refresh."
    />
  </UCard>
</template>
