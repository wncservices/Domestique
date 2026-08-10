<script setup lang="ts">
import { computed, ref } from 'vue'
import { useToast } from '@nuxt/ui/composables'
import { api } from '@/api/client'
import type { Account, Me, Provider } from '@/api/types'

const props = defineProps<{ accounts: Account[]; me?: Me | null; canManage: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const toast = useToast()

const linking = ref<Provider | null>(null)
const unlinking = ref('')
const error = ref('')

const providers: { id: Provider; name: string; icon: string }[] = [
  { id: 'garmin', name: 'Garmin', icon: 'i-lucide-watch' },
  { id: 'wahoo', name: 'Wahoo', icon: 'i-lucide-gauge' },
]

/**
 * Providers you link by signing in rather than by pressing a button here.
 *
 * Garmin used to be in this list, and pressing "Link Garmin" created a head
 * unit with no account behind it — a push target that could only ever fail.
 * Signing in on the card below does both, so the button would be a second,
 * worse way to do the same thing. Wahoo joins this list when its adapter does.
 */
const signInProviders: Provider[] = ['garmin']

/** One account per rider per provider, so hide what is already linked. */
const linkable = computed(() =>
  providers.filter(
    (p) =>
      !signInProviders.includes(p.id) &&
      !props.accounts.some((a) => a.provider === p.id && isMine(a)),
  ),
)

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
            @click="link(provider.id)"
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
          ? 'Sign in to Garmin below to add one. Until then there is nowhere to push routes.'
          : 'Nothing is linked yet, so there is nowhere to push routes.'
      "
    />
  </UCard>
</template>
