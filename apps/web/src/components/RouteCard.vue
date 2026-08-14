<script setup lang="ts">
import { computed, ref } from 'vue'
import { useToast } from '@nuxt/ui/composables'
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
const emit = defineEmits<{ deleted: []; updated: [] }>()

const toast = useToast()

const distance = computed(() => `${(props.route.distanceM / 1000).toFixed(1)} km`)
const ascent = computed(() => `${Math.round(props.route.ascentM)} m`)
const gpxUrl = computed(() => api.gpxUrl(props.route.slug))

const confirming = ref(false)
const deleting = ref(false)

/**
 * Mirrors the server's rule: riders may only edit what they uploaded, admins
 * anything. Delete and target editing share the same rule server-side
 * (`mayEdit`) — this just avoids offering buttons that would come back 403.
 */
const canEdit = computed(() => {
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

const editingTargets = ref(false)
const draftTargets = ref<string[]>([])
const savingTargets = ref(false)

const targetOptions = computed(() =>
  props.accounts.map((a) => ({ label: a.label || a.id, value: a.id })),
)

function openTargets() {
  draftTargets.value = [...props.route.targets]
  editingTargets.value = true
}

async function saveTargets() {
  savingTargets.value = true
  try {
    await api.updateTargets(props.route.slug, draftTargets.value)
    toast.add({
      title: 'Targets updated',
      description: draftTargets.value.length
        ? `“${props.route.name}” will sync to ${draftTargets.value.length} account${draftTargets.value.length === 1 ? '' : 's'}.`
        : `“${props.route.name}” will not sync anywhere until targets are set again.`,
      icon: 'i-lucide-check',
      color: 'success',
    })
    editingTargets.value = false
    emit('updated')
  } catch (err) {
    toast.add({
      title: 'Could not update targets',
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    savingTargets.value = false
  }
}

async function remove() {
  deleting.value = true
  try {
    await api.remove(props.route.slug)
    confirming.value = false
    toast.add({
      title: 'Route deleted',
      description: `“${props.route.name}” will be removed from the devices on the next push.`,
      icon: 'i-lucide-trash-2',
      color: 'success',
    })
    emit('deleted')
  } catch (err) {
    toast.add({
      title: 'Could not delete the route',
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <UCard variant="outline" class="app-card-interactive flex flex-col" :ui="{ body: 'flex-1 flex flex-col gap-3' }">
    <TrackPreview :slug="route.slug" />

    <div>
      <h3 class="font-medium text-highlighted">{{ route.name }}</h3>
      <p class="font-mono text-xs text-muted">{{ route.slug }}</p>
    </div>

    <p v-if="route.description" class="text-sm text-toned">{{ route.description }}</p>

    <dl class="flex gap-5">
      <div>
        <dt class="text-[0.7rem] uppercase tracking-wide text-dimmed">Distance</dt>
        <dd class="tabular-nums">{{ distance }}</dd>
      </div>
      <div>
        <dt class="text-[0.7rem] uppercase tracking-wide text-dimmed">Ascent</dt>
        <dd class="tabular-nums">{{ ascent }}</dd>
      </div>
      <div>
        <dt class="text-[0.7rem] uppercase tracking-wide text-dimmed">Points</dt>
        <dd class="tabular-nums">{{ route.pointCount.toLocaleString() }}</dd>
      </div>
    </dl>

    <div v-if="route.tags.length" class="flex flex-wrap gap-1.5">
      <UBadge v-for="tag in route.tags" :key="tag" color="neutral" variant="soft" size="sm">
        {{ tag }}
      </UBadge>
    </div>

    <UAlert
      v-if="route.unknownTargets.length"
      color="warning"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :title="`Unknown target${route.unknownTargets.length === 1 ? '' : 's'}`"
      :description="`${route.unknownTargets.join(', ')} — this route will never sync there.`"
      :ui="{ title: 'text-sm', description: 'text-xs' }"
    />

    <div class="mt-auto flex flex-wrap items-center gap-1.5 pt-1">
      <SyncBadge
        v-for="status in route.syncState"
        :key="status.accountId"
        :status="status"
        :account="accountFor(status.accountId)"
      />
      <UBadge v-if="!route.syncState.length" color="neutral" variant="outline" size="sm">
        no targets
      </UBadge>

      <span class="flex-1" />

      <UTooltip v-if="route.owner" :text="`Uploaded by ${route.owner}`">
        <UBadge color="neutral" variant="ghost" size="sm" icon="i-lucide-user">
          {{ route.owner }}
        </UBadge>
      </UTooltip>

      <UButton
        :href="gpxUrl"
        download
        icon="i-lucide-download"
        color="neutral"
        variant="ghost"
        size="xs"
        aria-label="Download GPX"
      />
      <UButton
        v-if="canEdit && targetOptions.length"
        icon="i-lucide-watch"
        color="neutral"
        variant="ghost"
        size="xs"
        aria-label="Choose target devices"
        @click="openTargets"
      />
      <UButton
        v-if="canEdit"
        icon="i-lucide-trash-2"
        color="error"
        variant="ghost"
        size="xs"
        aria-label="Delete route"
        @click="confirming = true"
      />
    </div>

    <UModal v-model:open="editingTargets" title="Choose target devices">
      <template #body>
        <div class="flex flex-col gap-3">
          <p class="text-sm text-toned">
            Which head units should “{{ route.name }}” sync to?
          </p>
          <UCheckboxGroup v-model="draftTargets" :items="targetOptions" />
          <p v-if="!draftTargets.length" class="text-xs text-dimmed">
            Nothing selected — this route will not sync to any account until targets are set
            again.
          </p>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :disabled="savingTargets"
            @click="editingTargets = false"
          >
            Cancel
          </UButton>
          <UButton :loading="savingTargets" @click="saveTargets">Save</UButton>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="confirming" title="Delete this route?">
      <template #body>
        <p class="text-sm text-toned">
          “{{ route.name }}” will be removed from the library, and queued for removal from
          every device that currently holds it.
        </p>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="ghost" :disabled="deleting" @click="confirming = false">
            Cancel
          </UButton>
          <UButton color="error" :loading="deleting" @click="remove">Delete</UButton>
        </div>
      </template>
    </UModal>
  </UCard>
</template>
