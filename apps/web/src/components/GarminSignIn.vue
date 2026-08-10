<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ApiError, api } from '@/api/client'
import type { GarminConnection } from '@/api/types'

/**
 * Signing in to Garmin, in a dialog off the Head units card.
 *
 * A dialog rather than a panel because linking a head unit is a thing you do
 * once and then forget: a permanent form for it sat on the page competing for
 * attention with the library, which is what people actually come here for.
 */
const props = defineProps<{ connection: GarminConnection }>()
const emit = defineEmits<{ changed: [GarminConnection] }>()

const open = defineModel<boolean>('open', { default: false })

const email = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')
// Two failures that are not "try again": an MFA challenge this flow cannot
// answer, and a block that never reached Garmin. Both get their own words —
// a red "check your password" would be advice that cannot work.
const kind = ref<'error' | 'mfa' | 'blocked'>('error')

const canSubmit = computed(() => !!email.value.trim() && !!password.value && !busy.value)

// Nothing from a previous attempt survives reopening the dialog — least of
// all a password sitting in a field nobody can see.
watch(open, (isOpen) => {
  if (!isOpen) {
    email.value = ''
    password.value = ''
    error.value = ''
    kind.value = 'error'
  }
})

async function connect() {
  busy.value = true
  error.value = ''
  kind.value = 'error'
  try {
    const connection = await api.garminConnect(email.value.trim(), password.value)
    email.value = ''
    password.value = ''
    open.value = false
    emit('changed', connection)
  } catch (err) {
    if (err instanceof ApiError && err.body.mfa === true) kind.value = 'mfa'
    else if (err instanceof ApiError && err.body.blocked === true) kind.value = 'blocked'
    if (kind.value !== 'error') password.value = ''
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" title="Sign in to Garmin">
    <template #body>
      <div class="flex flex-col gap-4">
        <UAlert
          v-if="kind === 'mfa'"
          color="warning"
          variant="subtle"
          icon="i-lucide-shield-alert"
          title="This account uses two-factor authentication"
          description="Domestique cannot answer the code challenge, so it cannot sign in to this account. Garmin offers no other way in for an app like this one."
        />
        <UAlert
          v-else-if="kind === 'blocked'"
          color="warning"
          variant="subtle"
          icon="i-lucide-shield-x"
          title="Garmin blocked the attempt"
          :description="error"
        />
        <UAlert
          v-else-if="error"
          color="error"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :description="error"
        />

        <!-- Not offered when it cannot work: an admin has not finished
             setting Garmin up, and nothing typed here would get through. -->
        <UAlert
          v-if="!props.connection.canConnect"
          color="neutral"
          variant="subtle"
          icon="i-lucide-key-round"
          :description="
            props.connection.unavailable ||
            'An administrator has not finished setting this up.'
          "
        />

        <form v-else class="flex flex-col gap-3" @submit.prevent="connect">
          <UFormField label="Garmin Connect email">
            <UInput
              v-model="email"
              type="email"
              autocomplete="username"
              placeholder="you@example.com"
              autofocus
              class="w-full"
            />
          </UFormField>
          <UFormField label="Password">
            <UInput
              v-model="password"
              type="password"
              autocomplete="current-password"
              class="w-full"
            />
          </UFormField>

          <p class="text-xs text-dimmed">
            Your password is not stored — it is used once to sign in, and your Edge becomes a
            place routes are sent.
          </p>

          <div class="flex justify-end gap-2 pt-1">
            <UButton color="neutral" variant="ghost" :disabled="busy" @click="open = false">
              Cancel
            </UButton>
            <UButton type="submit" icon="i-lucide-log-in" :loading="busy" :disabled="!canSubmit">
              Sign in
            </UButton>
          </div>
        </form>
      </div>
    </template>
  </UModal>
</template>
