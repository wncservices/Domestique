<script setup lang="ts">
import { computed, ref } from 'vue'
import { useLibrary } from '@/composables/useLibrary'
import LibraryDuplicatesPanel from '@/components/LibraryDuplicatesPanel.vue'
import PlanPanel from '@/components/PlanPanel.vue'
import RouteCard from '@/components/RouteCard.vue'

const {
  accounts,
  routes,
  problems,
  plan,
  loading,
  me,
  canPush,
  canUpload,
  refresh,
} = useLibrary()

const search = ref('')
const pushing = ref(false)
const failures = ref<string[]>([])

const visibleRoutes = computed(() => {
  const needle = search.value.trim().toLowerCase()
  if (!needle) return routes.value
  return routes.value.filter(
    (route) =>
      route.name.toLowerCase().includes(needle) ||
      route.slug.toLowerCase().includes(needle) ||
      route.description.toLowerCase().includes(needle) ||
      route.tags.some((tag) => tag.toLowerCase().includes(needle)),
  )
})

async function push(items: { accountId: string; slug: string }[]) {
  pushing.value = true
  failures.value = []
  try {
    const { api } = await import('@/api/client')
    const result = await api.push(items)
    failures.value = result.failures
    await refresh()
  } catch (err) {
    failures.value = [err instanceof Error ? err.message : String(err)]
  } finally {
    pushing.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <UAlert
      v-for="problem in problems"
      :key="problem"
      color="warning"
      variant="subtle"
      icon="i-lucide-file-warning"
      :description="problem"
    />

    <PlanPanel
      :plan="plan"
      :accounts="accounts"
      :pushing="pushing"
      :failures="failures"
      :can-push="canPush"
      @push="push"
      @refresh="refresh"
    />

    <!-- Cross-rider by nature (the same route can turn up uploaded by two
         different identities) — the same reason the endpoint behind this
         is admin-scoped rather than "my own routes". -->
    <LibraryDuplicatesPanel v-if="me?.role === 'admin'" @changed="refresh" />

    <section>
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 class="font-medium text-highlighted">
          {{ routes.length }} route{{ routes.length === 1 ? '' : 's' }}
        </h2>
        <UInput
          v-model="search"
          icon="i-lucide-search"
          placeholder="Filter by name, slug, tag or description"
          class="w-full sm:w-72"
        />
      </div>

      <div v-if="loading" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <USkeleton v-for="n in 3" :key="n" class="h-72" />
      </div>

      <UEmpty
        v-else-if="!routes.length"
        icon="i-lucide-route"
        title="No routes yet"
        description="Add one from the Add route page, or import from Komoot."
      >
        <template #actions>
          <UButton to="/add" icon="i-lucide-plus">Add a route</UButton>
        </template>
      </UEmpty>

      <UEmpty
        v-else-if="!visibleRoutes.length"
        icon="i-lucide-search-x"
        title="Nothing matches"
        :description="`No route matches “${search}”.`"
      />

      <div v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <RouteCard
          v-for="route in visibleRoutes"
          :key="route.slug"
          :route="route"
          :accounts="accounts"
          :writable="canUpload"
          :me="me"
          @deleted="refresh"
          @updated="refresh"
        />
      </div>
    </section>
  </div>
</template>
