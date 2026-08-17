<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useToast } from '@nuxt/ui/composables'
import { api } from '@/api/client'
import type { Crew } from '@/api/types'
import { useLibrary } from '@/composables/useLibrary'

const { crews, accounts, routes, me, loading, error, refresh } = useLibrary()
const toast = useToast()

// Every rider identifier the app already knows about, from data already
// fetched for this page — a linked account's owner, a route's owner, or a
// crew's owner. Not a full directory (a rider who has never uploaded,
// linked, or created anything won't appear), just enough to make adding
// someone who already has is a pick instead of a guess at exact spelling.
const knownRiders = computed(() => {
  const set = new Set<string>()
  for (const a of accounts.value) if (a.rider) set.add(a.rider)
  for (const r of routes.value) if (r.owner) set.add(r.owner)
  for (const c of crews.value) if (c.owner) set.add(c.owner)
  return [...set].sort((a, b) => a.localeCompare(b))
})

// Riders worth suggesting for this particular crew: already-approved
// members are pointless to re-suggest (adding one is a 409), but a rider
// with a pending request stays in the list on purpose — picking them there
// approves the request in the same step, cheaper than approve-from-the-
// pending-row-below for a rider you already meant to add.
function suggestedRiders(crew: Crew): string[] {
  const approved = new Set(
    (crew.members ?? []).filter((m) => m.status === 'approved').map((m) => m.rider),
  )
  return knownRiders.value.filter((rider) => !approved.has(rider))
}

onMounted(refresh)

function membershipLabel(crew: Crew): string {
  switch (crew.membershipStatus) {
    case 'approved':
      return 'Member'
    case 'pending':
      return 'Pending'
    default:
      return ''
  }
}

// --- create ---

const createOpen = ref(false)
const createName = ref('')
const creating = ref(false)
const createError = ref('')

async function createCrew() {
  if (!createName.value.trim()) return
  creating.value = true
  createError.value = ''
  try {
    await api.createCrew({ name: createName.value.trim() })
    toast.add({ title: `Created ${createName.value.trim()}`, icon: 'i-lucide-users-round', color: 'success' })
    createOpen.value = false
    createName.value = ''
    await refresh()
  } catch (err) {
    createError.value = err instanceof Error ? err.message : String(err)
  } finally {
    creating.value = false
  }
}

// --- join / leave ---

const joining = ref('')

async function join(crew: Crew) {
  joining.value = crew.id
  try {
    await api.joinCrew(crew.id)
    toast.add({ title: `Requested to join ${crew.name}`, icon: 'i-lucide-hand', color: 'success' })
    await refresh()
  } catch (err) {
    toast.add({
      title: 'Could not request to join',
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    joining.value = ''
  }
}

const removing = ref('')

async function removeMember(crew: Crew, rider: string) {
  removing.value = `${crew.id}:${rider}`
  try {
    await api.removeCrewMember(crew.id, rider)
    await refresh()
  } catch (err) {
    toast.add({
      title: `Could not remove ${rider}`,
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    removing.value = ''
  }
}

// --- add member directly (the owner's other way in, no request needed) ---

const addMemberInput = ref<Record<string, string>>({})
const addingMember = ref('')

async function addMember(crew: Crew) {
  const rider = (addMemberInput.value[crew.id] ?? '').trim()
  if (!rider) return
  addingMember.value = crew.id
  try {
    await api.addCrewMember(crew.id, rider)
    toast.add({ title: `${rider} added to ${crew.name}`, icon: 'i-lucide-user-plus', color: 'success' })
    addMemberInput.value[crew.id] = ''
    await refresh()
  } catch (err) {
    toast.add({
      title: `Could not add ${rider}`,
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    addingMember.value = ''
  }
}

const approving = ref('')

async function approveMember(crew: Crew, rider: string) {
  approving.value = `${crew.id}:${rider}`
  try {
    await api.approveCrewMember(crew.id, rider)
    toast.add({ title: `${rider} approved`, icon: 'i-lucide-check', color: 'success' })
    await refresh()
  } catch (err) {
    toast.add({
      title: `Could not approve ${rider}`,
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    approving.value = ''
  }
}

// --- delete ---

const deleteTarget = ref<Crew | null>(null)
const deleting = ref(false)

async function deleteCrew() {
  if (!deleteTarget.value) return
  deleting.value = true
  try {
    await api.deleteCrew(deleteTarget.value.id)
    toast.add({ title: `Deleted ${deleteTarget.value.name}`, icon: 'i-lucide-trash-2', color: 'success' })
    deleteTarget.value = null
    await refresh()
  } catch (err) {
    toast.add({
      title: 'Could not delete the crew',
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    deleting.value = false
  }
}

const pendingFor = (crew: Crew) => crew.members?.filter((m) => m.status === 'pending') ?? []
const approvedFor = (crew: Crew) => crew.members?.filter((m) => m.status === 'approved') ?? []
</script>

<template>
  <div class="flex flex-col gap-6">
    <UCard variant="outline">
      <template #header>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="flex items-center gap-2 font-medium text-highlighted">
              <UIcon name="i-lucide-users-round" />
              Crews
            </h2>
            <p class="text-sm text-muted">
              Share routes with riders you trust — a route reaches only its owner's own devices
              until it's shared to a crew.
            </p>
          </div>
          <UButton icon="i-lucide-plus" @click="createOpen = true">Create crew</UButton>
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

      <div v-if="crews.length" class="flex flex-col divide-y divide-default">
        <div v-for="crew in crews" :key="crew.id" class="flex flex-col gap-3 py-3 first:pt-0 last:pb-0">
          <div class="flex flex-wrap items-center gap-3">
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm text-highlighted">{{ crew.name }}</p>
              <p class="truncate font-mono text-xs text-dimmed">{{ crew.id }} · owner {{ crew.owner }}</p>
            </div>
            <UBadge color="neutral" variant="subtle" size="sm">
              {{ crew.memberCount }} member{{ crew.memberCount === 1 ? '' : 's' }}
            </UBadge>

            <template v-if="!crew.mine">
              <UButton
                v-if="crew.membershipStatus === 'none'"
                size="sm"
                icon="i-lucide-hand"
                :loading="joining === crew.id"
                @click="join(crew)"
              >
                Request to join
              </UButton>
              <UBadge v-else-if="crew.membershipStatus === 'pending'" color="warning" variant="subtle" size="sm">
                {{ membershipLabel(crew) }}
              </UBadge>
              <template v-else-if="crew.membershipStatus === 'approved'">
                <UBadge color="success" variant="subtle" size="sm">{{ membershipLabel(crew) }}</UBadge>
                <UButton
                  size="sm"
                  color="neutral"
                  variant="ghost"
                  icon="i-lucide-log-out"
                  :loading="removing === `${crew.id}:${me?.user}`"
                  @click="removeMember(crew, me?.user ?? '')"
                >
                  Leave
                </UButton>
              </template>
            </template>

            <UButton
              v-else
              size="sm"
              color="error"
              variant="ghost"
              icon="i-lucide-trash-2"
              @click="deleteTarget = crew"
            >
              Delete
            </UButton>
          </div>

          <!-- Owner's own view: add someone directly, see who's waiting, who's in. -->
          <div v-if="crew.mine" class="ml-1 flex flex-col gap-2 border-l-2 border-default pl-4">
            <form
              class="flex items-center gap-2"
              @submit.prevent="addMember(crew)"
            >
              <USelectMenu
                v-model="addMemberInput[crew.id]"
                :items="suggestedRiders(crew)"
                create-item="always"
                placeholder="Search or add a rider by username"
                icon="i-lucide-search"
                size="sm"
                class="max-w-xs"
                @create="(rider: string) => (addMemberInput[crew.id] = rider)"
              />
              <UButton
                type="submit"
                size="xs"
                icon="i-lucide-user-plus"
                :loading="addingMember === crew.id"
                :disabled="!addMemberInput[crew.id]?.trim()"
              >
                Add
              </UButton>
            </form>

            <div
              v-for="member in pendingFor(crew)"
              :key="`pending-${member.rider}`"
              class="flex items-center gap-2 text-sm"
            >
              <UIcon name="i-lucide-clock" class="size-4 text-dimmed" />
              <span class="flex-1 text-toned">{{ member.rider }} wants to join</span>
              <UButton
                size="xs"
                icon="i-lucide-check"
                :loading="approving === `${crew.id}:${member.rider}`"
                @click="approveMember(crew, member.rider)"
              >
                Approve
              </UButton>
              <UButton
                size="xs"
                color="neutral"
                variant="ghost"
                icon="i-lucide-x"
                :loading="removing === `${crew.id}:${member.rider}`"
                @click="removeMember(crew, member.rider)"
              >
                Deny
              </UButton>
            </div>

            <div
              v-for="member in approvedFor(crew)"
              :key="`approved-${member.rider}`"
              class="flex items-center gap-2 text-sm"
            >
              <UIcon name="i-lucide-user-check" class="size-4 text-dimmed" />
              <span class="flex-1 text-toned">{{ member.rider }}</span>
              <UButton
                v-if="member.rider.toLowerCase() !== crew.owner.toLowerCase()"
                size="xs"
                color="neutral"
                variant="ghost"
                icon="i-lucide-x"
                :loading="removing === `${crew.id}:${member.rider}`"
                @click="removeMember(crew, member.rider)"
              >
                Remove
              </UButton>
            </div>

            <p v-if="!pendingFor(crew).length && !approvedFor(crew).length" class="text-xs text-dimmed">
              Nobody else has joined yet.
            </p>
          </div>
        </div>
      </div>

      <UEmpty
        v-else-if="!loading"
        icon="i-lucide-users-round"
        title="No crews yet"
        description="Create one to start sharing routes with other riders."
      />
    </UCard>

    <UModal v-model:open="createOpen" title="Create a crew">
      <template #body>
        <UAlert
          v-if="createError"
          color="error"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :description="createError"
          class="mb-4"
        />
        <form class="flex flex-col gap-3" @submit.prevent="createCrew">
          <UFormField label="Name">
            <UInput v-model="createName" placeholder="Sunday club" class="w-full" />
          </UFormField>
          <p class="text-xs text-dimmed">
            You'll be its owner and first member — you can approve who else joins from here.
          </p>
          <div class="flex justify-end gap-2 pt-2">
            <UButton color="neutral" variant="ghost" @click="createOpen = false">Cancel</UButton>
            <UButton type="submit" icon="i-lucide-plus" :loading="creating" :disabled="!createName.trim()">
              Create
            </UButton>
          </div>
        </form>
      </template>
    </UModal>

    <UModal :open="!!deleteTarget" title="Delete this crew?" @update:open="deleteTarget = null">
      <template #body>
        <p class="text-sm text-toned">
          “{{ deleteTarget?.name }}” will be removed, along with its membership. Routes shared to
          it will stop reaching its members on the next push — nothing about the routes
          themselves changes.
        </p>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="ghost" :disabled="deleting" @click="deleteTarget = null">
            Cancel
          </UButton>
          <UButton color="error" :loading="deleting" @click="deleteCrew">Delete</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>
