<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useToast } from '@nuxt/ui/composables'
import { api, ApiError } from '@/api/client'
import type { GarminConnection, KomootConnection, MfaEnrollment, WahooConnection } from '@/api/types'
import { useLibrary } from '@/composables/useLibrary'
import AccountsPanel from '@/components/AccountsPanel.vue'
import GarminSetup from '@/components/GarminSetup.vue'
import KomootConnect from '@/components/KomootConnect.vue'
import WahooConnect from '@/components/WahooConnect.vue'

const { accounts, me, config, canManageAccounts, canImportKomoot, komootEnabled, refresh } =
  useLibrary()
const toast = useToast()

// --- profile: name + password, both proxied through Auth0's Management API
// (see meDTO.canEditName/canChangePassword on the server) ---

const nameInput = ref('')
const savingName = ref(false)

// Keep the field in step with the signed-in identity — most relevantly
// right after a save round-trips through refresh().
watch(
  () => me.value?.name,
  (name) => {
    nameInput.value = name ?? ''
  },
  { immediate: true },
)

async function saveName() {
  const name = nameInput.value.trim()
  if (!name || name === me.value?.name) return
  savingName.value = true
  try {
    await api.updateMe(name)
    await refresh()
    toast.add({ title: 'Name updated', icon: 'i-lucide-check', color: 'success' })
  } catch (err) {
    toast.add({
      title: 'Could not update your name',
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    savingName.value = false
  }
}

const sendingPasswordReset = ref(false)

async function sendPasswordReset() {
  sendingPasswordReset.value = true
  try {
    await api.sendPasswordReset()
    toast.add({
      title: 'Password reset email sent',
      description: `Check ${me.value?.email} for a link to set a new password.`,
      icon: 'i-lucide-mail-check',
      color: 'success',
    })
  } catch (err) {
    toast.add({
      title: 'Could not send the reset email',
      description: err instanceof ApiError ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    sendingPasswordReset.value = false
  }
}

// --- two-factor authentication: this app never renders a QR code itself —
// enrolling and confirming a new factor both happen on Auth0's own hosted
// Guardian page, reached through a one-time ticket URL. This app only lists
// what's already enrolled and lets the rider remove a factor. ---

const mfaEnrollments = ref<MfaEnrollment[]>([])
const loadingMfa = ref(false)
const enrolling = ref(false)
const removingFactorId = ref('')
const removeTarget = ref<MfaEnrollment | null>(null)
const removingFactor = ref(false)
// Set whenever a ticket URL might not have opened on its own — see
// startMfaEnroll. A real <a> the rider clicks themselves always works,
// unlike a second window.open attempt.
const enrollUrl = ref('')

const factorLabels: Record<string, string> = {
  totp: 'Authenticator app',
  sms: 'Text message',
  email: 'Email',
  'push-notification': 'Push notification',
  'webauthn-roaming': 'Security key',
  'webauthn-platform': 'Device passkey',
  'recovery-code': 'Recovery codes',
}

function factorLabel(type: string): string {
  return factorLabels[type] ?? type
}

async function loadMfa() {
  if (!me.value?.canEditName) return
  loadingMfa.value = true
  try {
    mfaEnrollments.value = await api.mfaEnrollments()
  } catch (err) {
    toast.add({
      title: 'Could not load two-factor authentication',
      description: err instanceof ApiError ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    loadingMfa.value = false
  }
}

// Opens Auth0's own hosted enrollment page in a new tab — nothing to embed,
// and the rider is still on Settings in this tab when they're done, so a
// manual refresh (rather than guessing when they've finished) picks it up.
//
// window.open here runs after an await, which means it is no longer inside
// the click's own user-activation window by the time the response comes
// back — browsers (Safari always, Chrome/Firefox often, depending on how
// long the round trip took) silently block it instead of opening the tab.
// There is no reliable cross-browser way to detect that after the fact
// (a blocked call can return either null or a window that immediately
// closes itself), so rather than guessing, a real link stays visible until
// the rider clicks it themselves — an actual click on an <a> is always a
// trusted user gesture, so it can never be blocked the way a second
// window.open attempt could be.
async function startMfaEnroll() {
  enrolling.value = true
  enrollUrl.value = ''
  try {
    const { ticketUrl } = await api.enrollMfa()
    window.open(ticketUrl, '_blank', 'noopener')
    enrollUrl.value = ticketUrl
    toast.add({
      title: 'Enrollment ready',
      description: 'Opened in a new tab — if you didn\'t see it, use the link below.',
      icon: 'i-lucide-external-link',
      color: 'info',
    })
  } catch (err) {
    toast.add({
      title: 'Could not start enrollment',
      description: err instanceof ApiError ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    enrolling.value = false
  }
}

async function removeMfaFactor() {
  const target = removeTarget.value
  if (!target) return
  removingFactor.value = true
  removingFactorId.value = target.id
  try {
    await api.removeMfaEnrollment(target.id)
    toast.add({ title: 'Removed', icon: 'i-lucide-check', color: 'success' })
    removeTarget.value = null
    await loadMfa()
  } catch (err) {
    toast.add({
      title: 'Could not remove it',
      description: err instanceof ApiError ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    removingFactor.value = false
    removingFactorId.value = ''
  }
}

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
  await Promise.all([loadConnection(), loadGarmin(), loadWahoo(), loadMfa()])
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <!-- Only ever available under authMode oidc, with Auth0 Management API
         credentials configured — meDTO omits both flags (defaulting them
         false) for a proxy/none deployment or one without that client. -->
    <UCard v-if="me?.canEditName" variant="outline">
      <template #header>
        <h2 class="flex items-center gap-2 font-medium text-highlighted">
          <UIcon name="i-lucide-user-round" />
          Profile
        </h2>
        <p class="text-sm text-muted">Your own name and sign-in, for this account only.</p>
      </template>

      <div class="flex flex-col gap-4">
        <UFormField label="Name">
          <div class="flex max-w-sm gap-2">
            <UInput v-model="nameInput" class="flex-1" @keyup.enter="saveName" />
            <UButton
              :loading="savingName"
              :disabled="!nameInput.trim() || nameInput.trim() === me?.name"
              @click="saveName"
            >
              Save
            </UButton>
          </div>
        </UFormField>

        <div>
          <p class="mb-1 text-sm font-medium text-toned">Password</p>
          <UButton
            v-if="me?.canChangePassword"
            size="sm"
            variant="soft"
            icon="i-lucide-mail"
            :loading="sendingPasswordReset"
            @click="sendPasswordReset"
          >
            Email me a reset link
          </UButton>
          <p v-else class="text-xs text-dimmed">
            You sign in with an external provider — there's no password here to change.
          </p>
        </div>

        <div>
          <div class="mb-1 flex items-center gap-2">
            <p class="text-sm font-medium text-toned">Two-factor authentication</p>
            <UButton
              size="xs"
              color="neutral"
              variant="ghost"
              icon="i-lucide-refresh-cw"
              :loading="loadingMfa"
              aria-label="Refresh"
              @click="loadMfa"
            />
          </div>

          <div v-if="mfaEnrollments.length" class="flex flex-col gap-2">
            <div
              v-for="factor in mfaEnrollments"
              :key="factor.id"
              class="flex items-center gap-2 text-sm"
            >
              <UIcon name="i-lucide-shield-check" class="size-4 text-dimmed" />
              <span class="flex-1 text-toned">
                {{ factorLabel(factor.type) }}<span v-if="factor.name"> · {{ factor.name }}</span>
              </span>
              <UBadge
                :color="factor.status === 'confirmed' ? 'success' : 'warning'"
                variant="subtle"
                size="sm"
              >
                {{ factor.status === 'confirmed' ? 'Active' : 'Pending' }}
              </UBadge>
              <UButton
                size="xs"
                color="neutral"
                variant="ghost"
                icon="i-lucide-x"
                :loading="removingFactor && removingFactorId === factor.id"
                @click="removeTarget = factor"
              />
            </div>
          </div>
          <p v-else class="mb-2 text-xs text-dimmed">Nothing set up yet.</p>

          <div class="mt-2 flex flex-wrap items-center gap-2">
            <UButton
              size="sm"
              variant="soft"
              icon="i-lucide-shield-plus"
              :loading="enrolling"
              @click="startMfaEnroll"
            >
              Add a factor
            </UButton>
            <!-- The new tab this opens on click can be silently blocked by
                 the browser (see startMfaEnroll's own comment) — this stays
                 up as a guaranteed-to-work fallback until the rider uses it
                 or starts a fresh attempt. -->
            <UButton
              v-if="enrollUrl"
              :to="enrollUrl"
              external
              target="_blank"
              rel="noopener noreferrer"
              size="sm"
              variant="outline"
              color="info"
              icon="i-lucide-external-link"
            >
              Continue in Auth0
            </UButton>
          </div>
        </div>
      </div>
    </UCard>

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

    <UModal
      :open="!!removeTarget"
      title="Remove this factor?"
      @update:open="removeTarget = null"
    >
      <template #body>
        <p class="text-sm text-toned">
          “{{ removeTarget ? factorLabel(removeTarget.type) : '' }}” will no longer be asked for at
          sign-in. Remove it only if you've lost access to it or no longer want it.
        </p>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="ghost" :disabled="removingFactor" @click="removeTarget = null">
            Cancel
          </UButton>
          <UButton color="error" :loading="removingFactor" @click="removeMfaFactor">Remove</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>
