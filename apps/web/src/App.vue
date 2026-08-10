<script setup lang="ts">
import { computed, onMounted } from 'vue'
import ColorModeToggle from '@/components/ColorModeToggle.vue'
import { useLibrary } from '@/composables/useLibrary'

const { me, accounts, routes, error, refresh, totalDistance, totalAscent, canUpload } = useLibrary()

const roleColor = computed(() => {
  switch (me.value?.role) {
    case 'admin':
      return 'primary' as const
    case 'rider':
      return 'success' as const
    default:
      return 'neutral' as const
  }
})

const stats = computed(() => [
  { label: 'Routes', value: String(routes.value.length), icon: 'i-lucide-route' },
  { label: 'Distance', value: `${totalDistance.value.toFixed(0)} km`, icon: 'i-lucide-ruler' },
  {
    label: 'Ascent',
    value: `${Math.round(totalAscent.value).toLocaleString()} m`,
    icon: 'i-lucide-mountain',
  },
  { label: 'Head units', value: String(accounts.value.length), icon: 'i-lucide-watch' },
])

// Add is hidden rather than disabled for a viewer: the page would be an empty
// form and an explanation, which is worse than not offering it.
const links = computed(() =>
  [
    { to: '/', label: 'Library', icon: 'i-lucide-route' },
    canUpload.value ? { to: '/add', label: 'Add route', icon: 'i-lucide-plus' } : null,
    { to: '/settings', label: 'Settings', icon: 'i-lucide-settings' },
  ].filter((link) => link !== null),
)

onMounted(refresh)
</script>

<template>
  <UApp>
    <div class="app-header sticky top-0 z-20">
      <UContainer class="max-w-5xl">
        <div class="flex items-center justify-between gap-4 py-3">
          <RouterLink to="/" class="flex min-w-0 items-center gap-3">
            <span
              class="flex size-9 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary"
              aria-hidden="true"
            >
              <UIcon name="i-lucide-bike" class="size-5" />
            </span>
            <div class="min-w-0">
              <h1 class="truncate text-base font-semibold tracking-tight text-highlighted">
                Domestique
              </h1>
              <p class="truncate text-xs text-muted">
                Shared route library, carried to every head unit.
              </p>
            </div>
          </RouterLink>

          <div class="flex shrink-0 items-center gap-2">
            <UBadge
              v-if="me?.authenticated"
              :color="roleColor"
              variant="subtle"
              icon="i-lucide-user"
              class="hidden sm:inline-flex"
            >
              {{ me.name || me.user }} · {{ me.role }}
            </UBadge>
            <UTooltip
              v-else-if="me"
              text="Anyone who can reach this page has full access. Put it behind Authelia before exposing it."
            >
              <UBadge color="warning" variant="subtle" icon="i-lucide-shield-off">
                <span class="hidden sm:inline">no login required</span>
                <span class="sm:hidden">open</span>
              </UBadge>
            </UTooltip>

            <!-- A plain link, not a fetch: signing out is the identity
                 provider ending its own session and redirecting, which an
                 XHR cannot do. -->
            <UButton
              v-if="me?.authenticated && me.logoutUrl"
              :to="me.logoutUrl"
              external
              icon="i-lucide-log-out"
              color="neutral"
              variant="ghost"
              size="sm"
              aria-label="Sign out"
            >
              <span class="hidden sm:inline">Sign out</span>
            </UButton>

            <ColorModeToggle />
          </div>
        </div>

        <nav class="flex gap-1 pb-1" aria-label="Sections">
          <UButton
            v-for="link in links"
            :key="link.to"
            :to="link.to"
            :icon="link.icon"
            :color="$route.path === link.to ? 'primary' : 'neutral'"
            :variant="$route.path === link.to ? 'subtle' : 'ghost'"
            size="sm"
          >
            {{ link.label }}
          </UButton>
        </nav>
      </UContainer>
    </div>

    <UContainer class="flex max-w-5xl flex-col gap-6 py-6">
      <section class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div v-for="stat in stats" :key="stat.label" class="app-card px-4 py-3">
          <div class="flex items-center gap-1.5 text-[0.7rem] uppercase tracking-wide text-dimmed">
            <UIcon :name="stat.icon" class="size-3.5" />
            {{ stat.label }}
          </div>
          <div class="mt-1 truncate text-2xl tabular-nums text-highlighted">
            {{ stat.value }}
          </div>
        </div>
      </section>

      <UAlert
        v-if="error"
        color="error"
        variant="subtle"
        icon="i-lucide-plug-zap"
        title="Could not reach the API"
        :description="error"
      />

      <RouterView />

      <!-- AGPL-3.0 section 13: a modified version offered over a network has
           to offer its users the source. A link in the footer of every page is
           the simplest way to actually satisfy that, and the easiest thing to
           forget. -->
      <footer class="mt-2 border-t border-default pt-4 text-xs text-dimmed">
        <a
          href="https://github.com/wncservices/Domestique"
          target="_blank"
          rel="noopener noreferrer"
          class="hover:text-default"
        >
          Domestique — free software under the AGPL-3.0. Source.
        </a>
      </footer>
    </UContainer>
  </UApp>
</template>
