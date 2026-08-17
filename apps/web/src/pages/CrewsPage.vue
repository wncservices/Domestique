<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useToast } from '@nuxt/ui/composables'
import { api } from '@/api/client'
import type { Crew } from '@/api/types'
import { useLibrary } from '@/composables/useLibrary'

const { crews, accounts, routes, me, loading, error, refresh, can } = useLibrary()
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

// --- manage members (owner/admin only): add/approve/remove and auto-share
// all live behind one popup instead of always-expanded inline, so a rider
// in several crews sees one row per crew rather than a growing list of
// member rosters underneath each one. ---

const manageTarget = ref<Crew | null>(null)
// Keeps the modal showing the crew it opened for even after `crews` is
// replaced wholesale by the next refresh() — matching by id, not identity.
const managingCrew = computed(() => crews.value.find((c) => c.id === manageTarget.value?.id) ?? null)

// --- auto-share (owner/admin only: changes what the crew does for every
// member's future uploads, not just the caller's own membership) ---

const togglingAutoShare = ref('')

async function toggleAutoShare(crew: Crew, autoShare: boolean) {
  togglingAutoShare.value = crew.id
  try {
    await api.setCrewAutoShare(crew.id, autoShare)
    toast.add({
      title: autoShare ? `New uploads will default to ${crew.name}` : `Auto-share turned off for ${crew.name}`,
      icon: 'i-lucide-share-2',
      color: 'success',
    })
    await refresh()
  } catch (err) {
    toast.add({
      title: 'Could not change auto-share',
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    togglingAutoShare.value = ''
  }
}

// --- share your own routes to a crew you belong to (existing routes;
// auto-share above only ever affects uploads made after it's turned on) ---

const shareTarget = ref<Crew | null>(null)
const shareSelections = ref<string[]>([])
const sharing = ref(false)

// Only routes the viewer may actually retarget — mirrors RouteCard's own
// canEdit rule (routes:edit-any, or routes:edit-own on a route they own).
// Someone else's routes aren't offered here even if they're also a member;
// the route's owner is the one who shares it. !!r.owner is required
// regardless of which permission branch matched: the server's own
// validateCrewTargets checks the route *owner's* crew membership, and an
// ownerless route (an import with no --owner) belongs to nobody, so it can
// never legally be shared to any crew — offering it here would just be a
// picker item that always 400s on save.
const myRoutes = computed(() =>
  routes.value.filter(
    (r) =>
      !!r.owner &&
      (can('routes:edit-any') ||
        (can('routes:edit-own') && r.owner.toLowerCase() === (me.value?.user ?? '').toLowerCase())),
  ),
)

const shareOptions = computed(() => myRoutes.value.map((r) => ({ label: r.name, value: r.slug })))

// True once every offered route is selected — drives the "Select all" /
// "Select none" toggle below without a separate ref to keep in sync.
const allRoutesSelected = computed(
  () => shareOptions.value.length > 0 && shareSelections.value.length === shareOptions.value.length,
)

function toggleSelectAllRoutes() {
  shareSelections.value = allRoutesSelected.value ? [] : shareOptions.value.map((o) => o.value)
}

function openShare(crew: Crew) {
  shareSelections.value = myRoutes.value.filter((r) => r.targets.includes(crew.id)).map((r) => r.slug)
  shareTarget.value = crew
}

async function saveShare() {
  const crew = shareTarget.value
  if (!crew) return
  sharing.value = true

  const changed = myRoutes.value.filter((r) => r.targets.includes(crew.id) !== shareSelections.value.includes(r.slug))
  const failures: string[] = []
  await Promise.all(
    changed.map(async (route) => {
      const wanted = shareSelections.value.includes(route.slug)
      const nextTargets = wanted
        ? [...route.targets, crew.id]
        : route.targets.filter((t) => t !== crew.id)
      try {
        await api.updateTargets(route.slug, nextTargets)
      } catch {
        failures.push(route.name)
      }
    }),
  )

  sharing.value = false
  if (failures.length) {
    toast.add({
      title: `Could not update ${failures.length} route${failures.length === 1 ? '' : 's'}`,
      description: failures.join(', '),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } else if (changed.length) {
    toast.add({
      title: `Updated ${changed.length} route${changed.length === 1 ? '' : 's'}`,
      icon: 'i-lucide-check',
      color: 'success',
    })
  }
  shareTarget.value = null
  await refresh()
}
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
        <div
          v-for="crew in crews"
          :key="crew.id"
          class="flex flex-wrap items-center gap-3 py-3 first:pt-0 last:pb-0"
        >
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm text-highlighted">{{ crew.name }}</p>
            <p class="truncate font-mono text-xs text-dimmed">{{ crew.id }} · owner {{ crew.owner }}</p>
          </div>

          <UBadge color="neutral" variant="subtle" size="sm">
            {{ crew.memberCount }} member{{ crew.memberCount === 1 ? '' : 's' }}
          </UBadge>
          <UTooltip v-if="crew.autoShare" text="New route uploads default to sharing here">
            <UBadge color="primary" variant="subtle" size="sm" icon="i-lucide-share-2">Auto-share</UBadge>
          </UTooltip>
          <UBadge v-if="crew.mine && pendingFor(crew).length" color="warning" variant="subtle" size="sm">
            {{ pendingFor(crew).length }} waiting
          </UBadge>

          <!-- Any approved member can share their own routes here, not
               just the owner — sharing a route only needs the route's
               owner to belong to the crew, the same rule the server
               enforces at write time. -->
          <UButton
            v-if="crew.membershipStatus === 'approved'"
            size="sm"
            variant="soft"
            icon="i-lucide-route"
            @click="openShare(crew)"
          >
            Share your routes
          </UButton>

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

          <!-- Owner's own view: everything that manages the roster (add,
               approve, remove, auto-share) lives behind one button instead
               of an always-expanded block — a rider who owns several crews
               would otherwise be scrolling past every member list on this
               page just to find the one they came for. -->
          <template v-else>
            <UButton size="sm" variant="soft" icon="i-lucide-settings-2" @click="manageTarget = crew">
              Manage members
            </UButton>
            <UButton
              size="sm"
              color="error"
              variant="ghost"
              icon="i-lucide-trash-2"
              @click="deleteTarget = crew"
            >
              Delete
            </UButton>
          </template>
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

    <UModal
      :open="!!shareTarget"
      :title="`Share routes to ${shareTarget?.name ?? ''}`"
      @update:open="shareTarget = null"
    >
      <template #body>
        <div class="flex flex-col gap-3">
          <div class="flex items-center justify-between gap-3">
            <p class="text-sm text-toned">
              Which of your own routes should reach {{ shareTarget?.name }}?
            </p>
            <UButton
              v-if="shareOptions.length"
              size="xs"
              color="neutral"
              variant="ghost"
              @click="toggleSelectAllRoutes"
            >
              {{ allRoutesSelected ? 'Select none' : 'Select all' }}
            </UButton>
          </div>
          <UCheckboxGroup
            v-if="shareOptions.length"
            v-model="shareSelections"
            :items="shareOptions"
            class="max-h-72 overflow-y-auto"
          />
          <p v-else class="text-xs text-dimmed">
            You don't have any routes of your own to share yet.
          </p>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="ghost" :disabled="sharing" @click="shareTarget = null">
            Cancel
          </UButton>
          <UButton :loading="sharing" :disabled="!shareOptions.length" @click="saveShare">Save</UButton>
        </div>
      </template>
    </UModal>

    <UModal
      :open="!!manageTarget"
      :title="`Manage ${manageTarget?.name ?? ''}`"
      @update:open="manageTarget = null"
    >
      <template #body>
        <div v-if="managingCrew" class="flex flex-col gap-4">
          <UTooltip
            text="When on, any new route a member uploads with no explicit sharing choice of their own is shared here automatically. Existing routes are never touched by this — turning it on doesn't reach back and share anything already uploaded."
          >
            <label class="flex w-fit items-center gap-2 text-sm text-toned">
              <USwitch
                :model-value="managingCrew.autoShare"
                :loading="togglingAutoShare === managingCrew.id"
                @update:model-value="(v: boolean) => toggleAutoShare(managingCrew!, v)"
              />
              Auto-share new uploads
            </label>
          </UTooltip>

          <form class="flex items-center gap-2" @submit.prevent="addMember(managingCrew)">
            <USelectMenu
              v-model="addMemberInput[managingCrew.id]"
              :items="suggestedRiders(managingCrew)"
              create-item="always"
              placeholder="Search or add a rider by username"
              icon="i-lucide-search"
              size="sm"
              class="max-w-xs"
              @create="(rider: string) => (addMemberInput[managingCrew!.id] = rider)"
            />
            <UButton
              type="submit"
              size="xs"
              icon="i-lucide-user-plus"
              :loading="addingMember === managingCrew.id"
              :disabled="!addMemberInput[managingCrew.id]?.trim()"
            >
              Add
            </UButton>
          </form>

          <div class="flex flex-col gap-2">
            <div
              v-for="member in pendingFor(managingCrew)"
              :key="`pending-${member.rider}`"
              class="flex items-center gap-2 text-sm"
            >
              <UIcon name="i-lucide-clock" class="size-4 text-dimmed" />
              <span class="flex-1 text-toned">{{ member.rider }} wants to join</span>
              <UButton
                size="xs"
                icon="i-lucide-check"
                :loading="approving === `${managingCrew.id}:${member.rider}`"
                @click="approveMember(managingCrew!, member.rider)"
              >
                Approve
              </UButton>
              <UButton
                size="xs"
                color="neutral"
                variant="ghost"
                icon="i-lucide-x"
                :loading="removing === `${managingCrew.id}:${member.rider}`"
                @click="removeMember(managingCrew!, member.rider)"
              >
                Deny
              </UButton>
            </div>

            <div
              v-for="member in approvedFor(managingCrew)"
              :key="`approved-${member.rider}`"
              class="flex items-center gap-2 text-sm"
            >
              <UIcon name="i-lucide-user-check" class="size-4 text-dimmed" />
              <span class="flex-1 text-toned">{{ member.rider }}</span>
              <UButton
                v-if="member.rider.toLowerCase() !== managingCrew.owner.toLowerCase()"
                size="xs"
                color="neutral"
                variant="ghost"
                icon="i-lucide-x"
                :loading="removing === `${managingCrew.id}:${member.rider}`"
                @click="removeMember(managingCrew!, member.rider)"
              >
                Remove
              </UButton>
            </div>

            <p v-if="!pendingFor(managingCrew).length && !approvedFor(managingCrew).length" class="text-xs text-dimmed">
              Nobody else has joined yet.
            </p>
          </div>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end">
          <UButton color="neutral" variant="ghost" @click="manageTarget = null">Done</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>
