<script setup lang="ts">
import { computed } from 'vue'
import { useColorMode } from '@/color-mode'

const { mode, resolved, cycle } = useColorMode()

// The icon shows the mode you are in, not the one you would move to. `system`
// gets its own icon so "following the OS" is distinguishable from "pinned to
// the same thing the OS happens to be doing".
const icon = computed(() => {
  if (mode.value === 'system') return 'i-lucide-monitor'
  return mode.value === 'dark' ? 'i-lucide-moon' : 'i-lucide-sun'
})

const label = computed(() =>
  mode.value === 'system' ? `Following the system (${resolved.value})` : `${mode.value} mode`,
)
</script>

<template>
  <UTooltip :text="`${label} — click to change`">
    <UButton
      :icon="icon"
      color="neutral"
      variant="ghost"
      :aria-label="`Colour mode: ${label}. Click to change.`"
      @click="cycle"
    />
  </UTooltip>
</template>
