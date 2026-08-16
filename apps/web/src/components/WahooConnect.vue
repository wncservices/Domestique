<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import type { WahooConnection } from '@/api/types'

const props = defineProps<{ connection: WahooConnection }>()
const emit = defineEmits<{ changed: [WahooConnection] }>()

const route = useRoute()
const busy = ref(false)
const error = ref('')

// Connecting is a redirect to Wahoo's own consent screen, not a form this
// page submits — the callback lands back wherever return_to says, validated
// server-side the same way /sso/login's does.
const connectHref = computed(() => `/wahoo/connect?return_to=${encodeURIComponent(route.fullPath)}`)

async function disconnect() {
  busy.value = true
  error.value = ''
  try {
    emit('changed', await api.wahooDisconnect())
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div>
    <UAlert
      v-if="error"
      color="error"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :description="error"
      class="mb-4"
    />

    <!-- Connected. -->
    <div v-if="props.connection.connected" class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex items-center gap-3">
        <UIcon name="i-lucide-circle-check" class="size-5 text-primary" />
        <div>
          <p class="text-sm text-highlighted">
            {{ props.connection.displayName || props.connection.email }}
          </p>
          <p v-if="props.connection.email" class="text-xs text-muted">{{ props.connection.email }}</p>
        </div>
      </div>
      <UButton
        icon="i-lucide-unlink"
        color="neutral"
        variant="ghost"
        :loading="busy"
        @click="disconnect"
      >
        Disconnect
      </UButton>
    </div>

    <!-- Nothing can be stored, so nothing is offered. -->
    <UAlert
      v-else-if="!props.connection.canConnect"
      color="neutral"
      variant="subtle"
      icon="i-lucide-key-round"
      title="Wahoo connections are unavailable"
      :description="
        props.connection.unavailable ||
        'This deployment has no encryption key, so a connection could not be kept safely.'
      "
    />

    <!-- Not connected, and it can be. -->
    <div v-else class="flex items-center justify-between gap-3">
      <p class="text-sm text-muted">Authorize Domestique to push routes to your Wahoo devices.</p>
      <UButton :to="connectHref" external icon="i-lucide-link"> Connect Wahoo </UButton>
    </div>
  </div>
</template>
