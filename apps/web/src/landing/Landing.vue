<script setup lang="ts">
import ColorModeToggle from '@/components/ColorModeToggle.vue'

// Where "Sign in" goes. The app lives behind Authelia on its own host, so this
// is a plain link across origins — Authelia takes the login and lands the
// rider in the app afterwards. Not a popup and not an embedded form: Authelia
// is a different origin that refuses to be framed, and an in-page password
// box pretending otherwise would be the wrong thing to teach people to trust.
//
// Built in, so a self-hoster changes it in one place.
const appURL = import.meta.env.VITE_APP_URL ?? 'https://app.domestique.dev'

const features = [
  {
    icon: 'i-lucide-upload',
    title: 'Upload once',
    body: 'Drop in a GPX, or import what you have already planned in Komoot. Everything lives in one shared library.',
  },
  {
    icon: 'i-lucide-corner-up-right',
    title: 'Turn-by-turn, not breadcrumbs',
    body: 'Routes become proper Garmin FIT courses, so a device can say something at a junction instead of drawing a line.',
  },
  {
    icon: 'i-lucide-server',
    title: 'Yours to run',
    body: 'Free software under the AGPL. Host it yourself, read every line, and keep your routes in your own database.',
  },
]
</script>

<template>
  <UApp>
    <div class="app-header sticky top-0 z-20">
      <UContainer class="flex max-w-5xl items-center justify-between gap-4 py-3">
        <div class="flex min-w-0 items-center gap-3">
          <span
            class="flex size-9 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary"
            aria-hidden="true"
          >
            <UIcon name="i-lucide-bike" class="size-5" />
          </span>
          <strong class="text-base font-semibold tracking-tight text-highlighted">
            Domestique
          </strong>
        </div>

        <div class="flex shrink-0 items-center gap-2">
          <ColorModeToggle />
          <UButton :to="appURL" icon="i-lucide-log-in" size="sm">Sign in</UButton>
        </div>
      </UContainer>
    </div>

    <UContainer class="max-w-5xl">
      <section class="py-16 sm:py-24">
        <h1
          class="max-w-2xl text-4xl font-semibold leading-[1.1] tracking-tight text-highlighted sm:text-6xl"
        >
          One route library.<br />Every head unit.
        </h1>

        <p class="mt-6 max-w-xl text-lg text-muted">
          You ride a Garmin, your mate rides a Wahoo. Add a route once and it turns up on
          both — no exporting, no cables, no “which file was the latest one?”
        </p>

        <div class="mt-8 flex flex-wrap items-center gap-3">
          <UButton :to="appURL" icon="i-lucide-log-in" size="lg" trailing-icon="i-lucide-arrow-right">
            Sign in
          </UButton>
          <UButton
            to="https://github.com/wncservices/Domestique"
            target="_blank"
            icon="i-lucide-github"
            color="neutral"
            variant="ghost"
            size="lg"
          >
            Source
          </UButton>
        </div>

        <p class="mt-4 text-sm text-dimmed">
          Sign-in is by invitation while this is still finding its feet.
        </p>
      </section>

      <section class="grid gap-4 pb-16 sm:grid-cols-3">
        <div v-for="feature in features" :key="feature.title" class="app-card p-5">
          <UIcon :name="feature.icon" class="size-5 text-primary" />
          <h2 class="mt-3 font-medium text-highlighted">{{ feature.title }}</h2>
          <p class="mt-1 text-sm text-muted">{{ feature.body }}</p>
        </div>
      </section>

      <footer class="border-t border-default py-6 text-xs text-dimmed">
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
