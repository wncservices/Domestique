<script setup lang="ts">
import { computed, ref } from 'vue'
import { ApiError, api } from '@/api/client'
import type { GarminConnection } from '@/api/types'

const props = defineProps<{ connection: GarminConnection }>()
const emit = defineEmits<{ changed: [GarminConnection] }>()

const email = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')
// Two-factor is not a failure to retry, so it is kept apart from the rest: a
// red "try again" box would be advice that cannot work.
const mfa = ref(false)

const expiresOn = computed(() => {
  if (!props.connection.expiresAt) return ''
  return new Date(props.connection.expiresAt).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  })
})

async function connect() {
  busy.value = true
  error.value = ''
  mfa.value = false
  try {
    const connection = await api.garminConnect(email.value.trim(), password.value)
    // Clear immediately on success. The password was only ever needed for the
    // one request; leaving it in a field keeps it in the page for no reason.
    email.value = ''
    password.value = ''
    emit('changed', connection)
  } catch (err) {
    if (err instanceof ApiError && err.body.mfa === true) {
      mfa.value = true
      // The password is wrong for nothing here, but it is still a password
      // sitting in a form that cannot be submitted usefully.
      password.value = ''
    }
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}

async function disconnect() {
  if (!confirm('Disconnect Garmin? Routes will stop syncing to it.')) return

  busy.value = true
  error.value = ''
  mfa.value = false
  try {
    emit('changed', await api.garminDisconnect())
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
      v-if="mfa"
      color="warning"
      variant="subtle"
      icon="i-lucide-shield-alert"
      title="This account uses two-factor authentication"
      description="Domestique cannot answer the code challenge, so it cannot sign in to this account. Garmin offers no other way in for an app like this one."
      class="mb-4"
    />
    <UAlert
      v-else-if="error"
      color="error"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :description="error"
      class="mb-4"
    />

    <!-- Connected. -->
    <div
      v-if="props.connection.connected"
      class="flex flex-wrap items-center justify-between gap-3"
    >
      <div class="flex items-center gap-3">
        <UIcon
          :name="props.connection.expired ? 'i-lucide-circle-alert' : 'i-lucide-circle-check'"
          class="size-5"
          :class="props.connection.expired ? 'text-warning' : 'text-primary'"
        />
        <div>
          <p class="text-sm text-highlighted">
            {{ props.connection.displayName || props.connection.email }}
          </p>
          <p class="text-xs text-muted">
            <template v-if="props.connection.expired">
              Signed out by Garmin — sign in again to resume syncing.
            </template>
            <template v-else-if="expiresOn"> Stays signed in until {{ expiresOn }}. </template>
          </p>
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

    <!-- Nothing can be stored or completed, so nothing is asked for. -->
    <UAlert
      v-else-if="!props.connection.canConnect"
      color="neutral"
      variant="subtle"
      icon="i-lucide-key-round"
      title="Garmin sign-in is unavailable"
      :description="props.connection.unavailable || 'An administrator has not finished setting this up.'"
    />

    <form
      v-if="props.connection.canConnect && !props.connection.connected"
      class="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end"
      @submit.prevent="connect"
    >
      <UFormField label="Email">
        <UInput
          v-model="email"
          type="email"
          autocomplete="username"
          placeholder="you@example.com"
          class="w-full"
        />
      </UFormField>
      <UFormField label="Password">
        <UInput v-model="password" type="password" autocomplete="current-password" class="w-full" />
      </UFormField>
      <UButton
        type="submit"
        icon="i-lucide-log-in"
        :loading="busy"
        :disabled="!email.trim() || !password"
      >
        Sign in
      </UButton>

      <!-- One line, because it answers a question the rider actually has.
           How the session is kept afterwards is not their problem. -->
      <p class="text-xs text-dimmed sm:col-span-3">
        Your Garmin password is not stored. Signing in adds your Edge as a place routes are
        sent.
      </p>
    </form>
  </div>
</template>
