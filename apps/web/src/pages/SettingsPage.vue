<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { GarminConnection, KomootConnection, WahooConnection } from '@/api/types'
import { useLibrary } from '@/composables/useLibrary'
import AccountsPanel from '@/components/AccountsPanel.vue'
import GarminSetup from '@/components/GarminSetup.vue'
import KomootConnect from '@/components/KomootConnect.vue'
import WahooConnect from '@/components/WahooConnect.vue'

const { accounts, me, config, canManageAccounts, canImportKomoot, komootEnabled, refresh } =
  useLibrary()

// Settings owns the Komoot connection now: this is the page where sign-ins
// live, and the Add page only consumes the result.
const connection = ref<KomootConnection>({ connected: false, shared: false, canConnect: false })
const connectionError = ref('')

const garmin = ref<GarminConnection>({ connected: false, canConnect: false })
const garminError = ref('')

const wahoo = ref<WahooConnection>({ connected: false, canConnect: false })
const wahooError = ref('')

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

async function loadWahoo() {
  if (!canManageAccounts.value) return
  try {
    wahoo.value = await api.wahooConnection()
  } catch (err) {
    wahooError.value = err instanceof Error ? err.message : String(err)
  }
}

// Connecting or disconnecting Wahoo links or unlinks the head unit, same as
// Garmin — the accounts list is stale the moment either changes.
async function wahooChanged(next: WahooConnection) {
  wahoo.value = next
  await refresh()
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
  await Promise.all([loadConnection(), loadGarmin(), loadWahoo()])
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <AccountsPanel
      :accounts="accounts"
      :me="me"
      :can-manage="canManageAccounts"
      :garmin="garmin"
      @changed="refresh"
      @garmin-changed="garminChanged"
    />

    <UAlert
      v-if="garminError"
      color="error"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :description="garminError"
    />

    <UCard v-if="canManageAccounts" variant="outline">
      <template #header>
        <h2 class="flex items-center gap-2 font-medium text-highlighted">
          <UIcon name="i-lucide-watch" />
          Wahoo
        </h2>
        <p class="text-sm text-muted">Connect your own Wahoo account to push routes to it.</p>
      </template>

      <UAlert
        v-if="wahooError"
        color="error"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :description="wahooError"
        class="mb-4"
      />

      <WahooConnect :connection="wahoo" @changed="wahooChanged" />
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

    <!-- Deployment plumbing, and only an admin gets it: the API omits the
         consumer entirely for everyone else, so this card does not exist for
         a rider. Nothing here is theirs to set or worth them knowing. -->
    <UCard v-if="garmin.consumer" variant="outline">
      <template #header>
        <h2 class="flex items-center gap-2 font-medium text-highlighted">
          <UIcon name="i-lucide-watch" />
          Garmin setup
        </h2>
        <p class="text-sm text-muted">
          One pair of app keys for the whole deployment, so riders can sign in.
        </p>
      </template>

      <GarminSetup :consumer="garmin.consumer" @changed="loadGarmin" />
    </UCard>

    <!-- Deployment plumbing, and only an admin gets it: the API omits
         Source entirely for everyone else (it names the database host and
         port), the same pattern the Garmin setup card above follows. -->
    <UCard v-if="config?.source" variant="outline">
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
          <dd class="font-mono text-xs break-all text-highlighted">{{ config.source }}</dd>
        </div>
      </dl>
    </UCard>
  </div>
</template>
