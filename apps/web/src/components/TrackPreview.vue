<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '@/api/client'

const props = defineProps<{ slug: string }>()

const points = ref<[number, number][]>([])
const failed = ref(false)

const WIDTH = 320
const HEIGHT = 160
const PADDING = 10

async function load() {
  failed.value = false
  points.value = []
  try {
    const track = await api.track(props.slug)
    points.value = track.points
  } catch {
    failed.value = true
  }
}

onMounted(load)
watch(() => props.slug, load)

/**
 * Projects lat/lon onto the viewbox. Longitude is scaled by cos(latitude) so a
 * route does not look stretched east-west — at Belgian latitudes a degree of
 * longitude is only ~63% of a degree of latitude.
 */
const path = computed(() => {
  if (points.value.length < 2) return ''

  const lats = points.value.map((p) => p[0])
  const midLat = (Math.min(...lats) + Math.max(...lats)) / 2
  const lonScale = Math.cos((midLat * Math.PI) / 180)

  const xs = points.value.map((p) => p[1] * lonScale)
  const minX = Math.min(...xs)
  const maxX = Math.max(...xs)
  const minY = Math.min(...lats)
  const maxY = Math.max(...lats)

  const spanX = maxX - minX || 1e-9
  const spanY = maxY - minY || 1e-9
  // One scale for both axes keeps the aspect ratio honest.
  const scale = Math.min((WIDTH - 2 * PADDING) / spanX, (HEIGHT - 2 * PADDING) / spanY)
  const offsetX = (WIDTH - spanX * scale) / 2
  const offsetY = (HEIGHT - spanY * scale) / 2

  return points.value
    .map((point, index) => {
      const x = offsetX + (point[1] * lonScale - minX) * scale
      // SVG y grows downward; latitude grows north, so flip it.
      const y = HEIGHT - offsetY - (point[0] - minY) * scale
      return `${index === 0 ? 'M' : 'L'}${x.toFixed(1)} ${y.toFixed(1)}`
    })
    .join(' ')
})

const start = computed(() => {
  const match = path.value.match(/^M([\d.]+) ([\d.]+)/)
  return match ? { x: Number(match[1]), y: Number(match[2]) } : null
})
</script>

<template>
  <div class="preview">
    <svg
      v-if="path"
      :viewBox="`0 0 ${WIDTH} ${HEIGHT}`"
      role="img"
      :aria-label="`Route shape for ${slug}`"
    >
      <path :d="path" class="track" />
      <circle v-if="start" :cx="start.x" :cy="start.y" r="4" class="start" />
    </svg>
    <p v-else-if="failed" class="empty">track unavailable</p>
    <p v-else class="empty">loading…</p>
  </div>
</template>

<style scoped>
.preview {
  aspect-ratio: 2 / 1;
  display: grid;
  place-items: center;
  background: var(--surface-sunken);
  border-radius: 8px;
  overflow: hidden;
}

svg {
  width: 100%;
  height: 100%;
}

.track {
  fill: none;
  stroke: var(--accent);
  stroke-width: 2.5;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.start {
  fill: var(--accent-strong);
}

.empty {
  color: var(--text-muted);
  font-size: 0.85rem;
  margin: 0;
}
</style>
