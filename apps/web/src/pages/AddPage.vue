<script setup lang="ts">
import { useLibrary } from '@/composables/useLibrary'
import GarminCoursesPanel from '@/components/GarminCoursesPanel.vue'
import KomootPanel from '@/components/KomootPanel.vue'
import UploadPanel from '@/components/UploadPanel.vue'

const { accounts, me, config, canUpload, canImportKomoot, canSyncGarmin, komootEnabled, refresh } =
  useLibrary()
</script>

<template>
  <div class="flex flex-col gap-6">
    <UploadPanel v-if="canUpload" :accounts="accounts" :me="me" @uploaded="refresh" />

    <UAlert
      v-else
      color="neutral"
      variant="subtle"
      icon="i-lucide-lock"
      title="You cannot add routes"
      description="Your role is read-only. An admin can change that in Authelia."
    />

    <KomootPanel
      v-if="canImportKomoot && komootEnabled && config"
      :state="config.komoot === 'disabled' ? 'unconfigured' : config.komoot"
      @imported="refresh"
    />

    <GarminCoursesPanel v-if="canSyncGarmin" @imported="refresh" />
  </div>
</template>
