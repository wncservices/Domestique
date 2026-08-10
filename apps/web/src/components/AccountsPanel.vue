<script setup lang="ts">
import { computed, ref } from 'vue'
import { useToast } from '@nuxt/ui/composables'
import { api } from '@/api/client'
import type { Account, GarminConnection, Me, Provider } from '@/api/types'
import GarminSignIn from '@/components/GarminSignIn.vue'

const props = defineProps<{
  accounts: Account[]
  me?: Me | null
  canManage: boolean
  garmin: GarminConnection
}>()
const emit = defineEmits<{ changed: []; garminChanged: [GarminConnection] }>()

// Garmin is linked by signing in, so its button opens a dialog instead of
// creating an account outright. Wahoo still links directly — there is no
// sign-in behind it yet, which is what the "adapter not wired up" badge in
// the list below is there to admit.
const signingIn = ref(false)

const toast = useToast()

const linking = ref<Provider | null>(null)
const unlinking = ref('')
const error = ref('')

const providers: { id: Provider; name: string; icon: string }[] = [
  { id: 'garmin', name: 'Garmin', icon: 'i-lucide-watch' },
  { id: 'wahoo', name: 'Wahoo', icon: 'i-lucide-gauge' },
]

/** One account per rider per provider, so hide what is already linked. */
const linkable = computed(() =>
  providers.filter((p) => !props.accounts.some((a) => a.provider === p.id && isMine(a))),
)

/** Garmin needs a password before it is a push target; Wahoo does not yet. */
function isSignIn(provider: Provider): boolean {
  return provider === 'garmin'
}

function onGarminChanged(connection: GarminConnection) {
  emit('garminChanged', connection)
  emit('changed')
}

function isMine(account: Account): boolean {
  const user = props.me?.user
  if (!user) return account.mine
  return account.rider.toLowerCase() === user.toLowerCase()
}

async function link(provider: Provider) {
  linking.value = provider
  error.value = ''
  try {
    const account = await api.linkAccount({ provider })
    toast.add({
      title: `${account.label} linked`,
      description: 'Routes targeting it will be pushed on the next sync.',
      icon: 'i-lucide-link',
      color: 'success',
    })
    emit('changed')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    linking.value = null
  }
}

async function unlink(account: Account) {
  if (!confirm(`Unlink ${account.label}? Routes will stop syncing to it.`)) return

  unlinking.value = account.id
  error.value = ''
  try {
    // Unlinking a Garmin has to forget the sign-in behind it too, or the
    // account comes back on the next sign-in attached to a session the rider
    // thought they had removed. The endpoint does both.
    if (account.provider === 'garmin' && isMine(account)) {
      emit('garminChanged', await api.garminDisconnect())
      toast.add({ title: `${account.label} unlinked`, icon: 'i-lucide-unlink', color: 'success' })
      emit('changed')
      return
    }
    await api.unlinkAccount(account.id)
    toast.add({ title: `${account.label} unlinked`, icon: 'i-lucide-unlink', color: 'success' })
    emit('changed')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    unlinking.value = ''
  }
}
</script>

<template>
  <UCard variant="outline">
    <template #header>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 class="font-medium text-highlighted">Head units</h2>
          <p class="text-sm text-muted">
            {{ accounts.length }} linked · routes are pushed to these
          </p>
        </div>
        <div v-if="canManage" class="flex gap-2">
          <UButton
            v-for="provider in linkable"
            :key="provider.id"
            :icon="provider.icon"
            color="neutral"
            variant="subtle"
            :loading="linking === provider.id"
            @click="isSignIn(provider.id) ? (signingIn = true) : link(provider.id)"
          >
            Link {{ provider.name }}
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

    <div v-if="accounts.length" class="flex flex-col divide-y divide-default">
      <div
        v-for="account in accounts"
        :key="account.id"
        class="flex flex-wrap items-center gap-3 py-2 first:pt-0 last:pb-0"
      >
        <UIcon
          :name="account.provider === 'garmin' ? 'i-lucide-watch' : 'i-lucide-gauge'"
          class="text-dimmed"
        />
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm text-highlighted">{{ account.label }}</p>
          <p class="font-mono text-xs text-dimmed">{{ account.id }}</p>
        </div>

        <UBadge v-if="!account.implemented" color="warning" variant="subtle" size="sm">
          adapter not wired up
        </UBadge>

        <UButton
          v-if="account.mine"
          icon="i-lucide-unlink"
          color="neutral"
          variant="ghost"
          size="xs"
          :loading="unlinking === account.id"
          aria-label="Unlink"
          @click="unlink(account)"
        />
      </div>
    </div>

    <UEmpty
      v-else
      icon="i-lucide-watch"
      title="No head units linked"
      :description="
        canManage
          ? 'Link a Garmin or Wahoo account above. Until then there is nowhere to push routes.'
          : 'Nothing is linked yet, so there is nowhere to push routes.'
      "
    />

    <GarminSignIn v-model:open="signingIn" :connection="props.garmin" @changed="onGarminChanged" />
  </UCard>
</template>
