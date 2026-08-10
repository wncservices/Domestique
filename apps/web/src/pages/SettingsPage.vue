<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { GarminConnection, KomootConnection } from '@/api/types'
import { useLibrary } from '@/composables/useLibrary'
import AccountsPanel from '@/components/AccountsPanel.vue'
import GarminConnect from '@/components/GarminConnect.vue'
import KomootConnect from '@/components/KomootConnect.vue'

const { accounts, me, config, canManageAccounts, canImportKomoot, komootEnabled, refresh } =
  useLibrary()

// Settings owns the Komoot connection now: this is the page where sign-ins
// live, and the Add page only consumes the result.
const connection = ref<KomootConnection>({ connected: false, shared: false, canConnect: false })
const connectionError = ref('')

const garmin = ref<GarminConnection>({ connected: false, canConnect: false })
const garminError = ref('')

async function loadConnection() {
  if (!komootEnabled.value || !canImportKomoot.value) return
  try {
    connection.value = await api.komootConnection()
  } catch (err) {
    connectionError.value = err instanceof Error ? err.message : String(err)
  }
}

async function loadGarmin() {
  if (!canManageAccounts.value) return
  try {
    garmin.value = await api.garminConnection()
  } catch (err) {
    garminError.value = err instanceof Error ? err.message : String(err)
  }
}

// Signing in to Garmin links the head unit, so the accounts list is stale the
// moment either of those changes.
async function garminChanged(next: GarminConnection) {
  garmin.value = next
  await refresh()
}

onMounted(async () => {
  // Wait for the shared state first. Whether Komoot is worth asking about
  // depends on the config and the caller's permissions, and mounting a page
  // races the shell's first fetch — without this the card renders and then
  // reports "no encryption key" because it never got to ask.
  await refresh()
  await Promise.all([loadConnection(), loadGarmin()])
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <AccountsPanel
      :accounts="accounts"
      :me="me"
      :can-manage="canManageAccounts"
      @changed="refresh"
    />

    <UCard v-if="canManageAccounts" id="garmin" variant="outline">
      <template #header>
        <h2 class="flex items-center gap-2 font-medium text-highlighted">
          <UIcon name="i-lucide-watch" />
          Garmin
        </h2>
        <p class="text-sm text-muted">
          Sign in with your Garmin Connect account to send routes to your Edge.
        </p>
      </template>

      <UAlert
        v-if="garminError"
        color="error"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :description="garminError"
        class="mb-4"
      />

      <GarminConnect :connection="garmin" @changed="garminChanged" />
    </UCard>

    <UCard v-if="canImportKomoot && komootEnabled" variant="outline">
      <template #header>
        <h2 class="flex items-center gap-2 font-medium text-highlighted">
          <UIcon name="i-lucide-mountain-snow" />
          Komoot
        </h2>
        <p class="text-sm text-muted">Sign in to import your own planned routes.</p>
      </template>

      <UAlert
        v-if="connectionError"
        color="error"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :description="connectionError"
        class="mb-4"
      />

      <KomootConnect :connection="connection" @changed="connection = $event" />
    </UCard>

    <UCard variant="outline">
      <template #header>
        <h2 class="flex items-center gap-2 font-medium text-highlighted">
          <UIcon name="i-lucide-info" />
          This deployment
        </h2>
      </template>

      <dl class="grid gap-3 text-sm sm:grid-cols-2">
        <div>
          <dt class="text-dimmed">Signed in as</dt>
          <dd class="text-highlighted">
            {{ me?.authenticated ? `${me.name || me.user} (${me.role})` : 'nobody — every visitor is an admin' }}
          </dd>
        </div>
        <div>
          <dt class="text-dimmed">Library</dt>
          <dd class="font-mono text-xs break-all text-highlighted">{{ config?.source }}</dd>
        </div>
      </dl>
    </UCard>
  </div>
</template>
