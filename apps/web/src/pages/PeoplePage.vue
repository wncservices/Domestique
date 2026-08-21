<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useToast } from '@nuxt/ui/composables'
import { api, ApiError } from '@/api/client'
import type { AssignableRole, Person } from '@/api/types'
import { roleColor } from '@/utils/role'
import { usePagedList } from '@/composables/usePagedList'

const toast = useToast()

const people = ref<Person[]>([])
const { page: peoplePage, paged: pagedPeople, pageSize: peoplePageSize } = usePagedList(people, 24)
const loading = ref(true)
const error = ref('')
// null (never configured) is distinct from "" (a real, empty error) — the
// page needs to tell "no Management API credentials set" apart from any
// other failure, and only the first one hides the invite form entirely
// rather than just showing an alert above it.
const unavailable = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  unavailable.value = false
  try {
    people.value = await api.people()
  } catch (err) {
    if (err instanceof ApiError && err.status === 412) {
      unavailable.value = true
    } else {
      error.value = err instanceof Error ? err.message : String(err)
    }
  } finally {
    loading.value = false
  }
}

onMounted(load)

const roleOptions: { label: string; value: AssignableRole }[] = [
  { label: 'Admin', value: 'admin' },
  { label: 'Rider', value: 'rider' },
  { label: 'Viewer', value: 'viewer' },
]

// --- invite ---

const inviting = ref(false)
const inviteOpen = ref(false)
const inviteError = ref('')
const inviteEmail = ref('')
const inviteName = ref('')
const inviteRole = ref<AssignableRole>('rider')

async function sendInvite() {
  if (!inviteEmail.value.trim()) return
  inviting.value = true
  inviteError.value = ''
  try {
    const result = await api.invitePerson({
      email: inviteEmail.value.trim(),
      name: inviteName.value.trim() || undefined,
      role: inviteRole.value,
    })
    if (result.error) {
      // The account exists and has its role — only the email failed. Worth
      // saying plainly rather than as a generic failure, since retrying the
      // whole invite would try (and fail) to create the same account again.
      toast.add({
        title: 'Account created, but the invite email failed',
        description: result.error,
        icon: 'i-lucide-triangle-alert',
        color: 'warning',
      })
    } else if (result.granted) {
      // They already had an Auth0 identity — most often a prior Google
      // sign-in — so this granted access to it directly rather than
      // creating a second account. No invite email goes out: they already
      // have a way to sign in.
      toast.add({
        title: `Granted access to ${result.person.email}`,
        description: 'They already had a sign-in for this address — no new account was created.',
        icon: 'i-lucide-shield-check',
        color: 'success',
      })
    } else {
      toast.add({
        title: `Invited ${result.person.email}`,
        icon: 'i-lucide-mail-check',
        color: 'success',
      })
    }
    inviteOpen.value = false
    inviteEmail.value = ''
    inviteName.value = ''
    inviteRole.value = 'rider'
    await load()
  } catch (err) {
    inviteError.value = err instanceof Error ? err.message : String(err)
  } finally {
    inviting.value = false
  }
}

// --- role changes ---

const changingRole = ref('')

async function changeRole(person: Person, role: AssignableRole) {
  if (role === person.role) return
  changingRole.value = person.id
  try {
    await api.setPersonRole(person.id, role)
    person.role = role
    toast.add({
      title: `${person.email} is now ${role}`,
      icon: 'i-lucide-shield-check',
      color: 'success',
    })
  } catch (err) {
    toast.add({
      title: `Could not change ${person.email}'s role`,
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    changingRole.value = ''
  }
}

function lastSeen(person: Person): string {
  if (!person.lastLogin) return 'never signed in'
  return new Date(person.lastLogin).toLocaleDateString()
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <UCard variant="outline">
      <template #header>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="flex items-center gap-2 font-medium text-highlighted">
              <UIcon name="i-lucide-users" />
              People
            </h2>
            <p class="text-sm text-muted">
              <template v-if="loading">Loading…</template>
              <template v-else-if="unavailable">Not available on this deployment</template>
              <template v-else>{{ people.length }} with access</template>
            </p>
          </div>
          <div class="flex gap-2">
            <UButton icon="i-lucide-refresh-cw" color="neutral" variant="ghost" :loading="loading" @click="load">
              Refresh
            </UButton>
            <UButton
              v-if="!unavailable"
              icon="i-lucide-user-plus"
              :disabled="loading"
              @click="inviteOpen = true"
            >
              Invite
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

      <UEmpty
        v-if="unavailable"
        icon="i-lucide-key-round"
        title="Auth0 Management API access is not configured"
        description="An administrator can set DOMESTIQUE_AUTH0_MGMT_CLIENT_ID and DOMESTIQUE_AUTH0_MGMT_CLIENT_SECRET to enable this page."
      />

      <template v-else-if="people.length">
        <div class="flex flex-col divide-y divide-default">
          <div
            v-for="person in pagedPeople"
            :key="person.id"
            class="flex flex-wrap items-center gap-3 py-2 first:pt-0 last:pb-0"
          >
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm text-highlighted">{{ person.name || person.email }}</p>
              <p class="truncate text-xs text-dimmed">{{ person.email }} · {{ lastSeen(person) }}</p>
            </div>
            <UBadge :color="roleColor(person.role)" variant="subtle" size="sm">{{ person.role }}</UBadge>
            <USelect
              :model-value="person.role"
              :items="roleOptions"
              :loading="changingRole === person.id"
              :disabled="changingRole === person.id"
              size="sm"
              class="w-28"
              aria-label="Change role"
              @update:model-value="(role: AssignableRole) => changeRole(person, role)"
            />
          </div>
        </div>

        <UPagination
          v-if="people.length > peoplePageSize"
          v-model:page="peoplePage"
          :total="people.length"
          :items-per-page="peoplePageSize"
          class="mt-4 justify-center"
        />
      </template>

      <UEmpty
        v-else-if="!loading"
        icon="i-lucide-user-x"
        title="Nobody has access yet"
        description="Invite the first rider above."
      />
    </UCard>

    <UModal v-model:open="inviteOpen" title="Invite someone">
      <template #body>
        <UAlert
          v-if="inviteError"
          color="error"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :description="inviteError"
          class="mb-4"
        />
        <form class="flex flex-col gap-3" @submit.prevent="sendInvite">
          <UFormField label="Email">
            <UInput v-model="inviteEmail" type="email" placeholder="rider@example.com" class="w-full" />
          </UFormField>
          <UFormField label="Name" hint="optional">
            <UInput v-model="inviteName" class="w-full" />
          </UFormField>
          <UFormField label="Role">
            <USelect v-model="inviteRole" :items="roleOptions" class="w-full" />
          </UFormField>
          <p class="text-xs text-dimmed">
            They'll get an email to set their own password. Nothing is shared over chat or in this app.
          </p>
          <div class="flex justify-end gap-2 pt-2">
            <UButton color="neutral" variant="ghost" @click="inviteOpen = false">Cancel</UButton>
            <UButton
              type="submit"
              icon="i-lucide-mail-plus"
              :loading="inviting"
              :disabled="!inviteEmail.trim()"
            >
              Send invite
            </UButton>
          </div>
        </form>
      </template>
    </UModal>
  </div>
</template>
