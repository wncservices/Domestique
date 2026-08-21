<script setup lang="ts">
import { onBeforeUnmount, onMounted, useTemplateRef, watch } from 'vue'
import { useColorMode } from '@/color-mode'
import type { Map as MapLibreMap, StyleSpecification } from 'maplibre-gl'

const props = defineProps<{
  routes: { slug: string; points: [number, number][] }[]
  selectedSlug?: string | null
}>()

const emit = defineEmits<{ select: [slug: string] }>()

const { resolved } = useColorMode()

const container = useTemplateRef<HTMLElement>('container')
let map: MapLibreMap | null = null

const SOURCE_ID = 'routes'
const LINE_LAYER_ID = 'routes-line'
const START_LAYER_ID = 'routes-start'

/**
 * pmtiles registers itself as a global maplibregl protocol handler, not a
 * per-map one — calling addProtocol twice across multiple RouteMap
 * instances on the same page (e.g. a library map plus a popup) is harmless
 * but pointless, so this only happens once per page load.
 */
let protocolReady: Promise<void> | null = null
function ensureProtocol(maplibregl: typeof import('maplibre-gl')) {
  if (!protocolReady) {
    protocolReady = import('pmtiles').then(({ Protocol }) => {
      const protocol = new Protocol()
      maplibregl.addProtocol('pmtiles', protocol.tile)
    })
  }
  return protocolReady
}

/**
 * The .pmtiles basemap is self-hosted specifically so route coordinates
 * never reach a third party (see tiles/AGENTS.md in domestique-infra) —
 * style/sprite/glyphs carry no location data, so those come from a public
 * CDN. Absolute URL, not a bare relative path: the pmtiles:// scheme wraps
 * a real fetchable URL, and building it from location.origin keeps this
 * working whether the app is reached at domestique.dev or app.domestique.dev.
 */
async function buildStyle(theme: 'light' | 'dark'): Promise<StyleSpecification> {
  const { layers, namedTheme } = await import('protomaps-themes-base')
  const basemapUrl = `pmtiles://${window.location.origin}/tiles/basemap.pmtiles`
  return {
    version: 8,
    glyphs: 'https://protomaps.github.io/basemaps-assets/fonts/{fontstack}/{range}.pbf',
    sprite: `https://protomaps.github.io/basemaps-assets/sprites/v4/${theme}`,
    sources: {
      protomaps: {
        type: 'vector',
        url: basemapUrl,
        attribution: '© <a href="https://openstreetmap.org">OpenStreetMap</a>',
      },
    },
    layers: layers('protomaps', namedTheme(theme), { lang: 'en' }),
  }
}

function toFeatureCollection(routes: typeof props.routes) {
  return {
    type: 'FeatureCollection' as const,
    features: routes
      .filter((r) => r.points.length > 1)
      .map((r) => ({
        type: 'Feature' as const,
        properties: { slug: r.slug },
        // TrackResponse points are [lat, lon]; GeoJSON wants [lon, lat].
        geometry: {
          type: 'LineString' as const,
          coordinates: r.points.map(([lat, lon]) => [lon, lat]),
        },
      })),
  }
}

function fitToRoutes(maplibregl: typeof import('maplibre-gl')) {
  if (!map || !props.routes.some((r) => r.points.length)) return
  const bounds = new maplibregl.LngLatBounds()
  for (const route of props.routes) {
    for (const [lat, lon] of route.points) bounds.extend([lon, lat])
  }
  if (!bounds.isEmpty()) map.fitBounds(bounds, { padding: 32, duration: 0, maxZoom: 16 })
}

/**
 * setStyle (used on a light/dark toggle, below) tears down every custom
 * source/layer along with the base style, so this runs again on every
 * 'style.load' — not just the first one — to put the route data back.
 */
function addRouteLayers() {
  if (!map) return
  map.addSource(SOURCE_ID, { type: 'geojson', data: toFeatureCollection(props.routes) })
  map.addLayer({
    id: LINE_LAYER_ID,
    type: 'line',
    source: SOURCE_ID,
    layout: { 'line-cap': 'round', 'line-join': 'round' },
    paint: {
      'line-color': '#16b8ab',
      'line-width': ['case', ['==', ['get', 'slug'], props.selectedSlug ?? ''], 5, 3],
      'line-opacity': ['case', ['==', ['get', 'slug'], props.selectedSlug ?? ''], 1, 0.75],
    },
  })
  map.addLayer({
    id: START_LAYER_ID,
    type: 'circle',
    source: SOURCE_ID,
    filter: ['==', ['geometry-type'], 'LineString'],
    paint: { 'circle-radius': 4, 'circle-color': '#16b8ab' },
  })

  map.on('click', LINE_LAYER_ID, (e) => {
    const slug = e.features?.[0]?.properties?.slug
    if (typeof slug === 'string') emit('select', slug)
  })
  map.on('mouseenter', LINE_LAYER_ID, () => {
    if (map) map.getCanvas().style.cursor = 'pointer'
  })
  map.on('mouseleave', LINE_LAYER_ID, () => {
    if (map) map.getCanvas().style.cursor = ''
  })
}

async function init() {
  if (!container.value) return
  const maplibregl = await import('maplibre-gl')
  await import('maplibre-gl/dist/maplibre-gl.css')
  await ensureProtocol(maplibregl)

  const theme = resolved.value === 'dark' ? 'dark' : 'light'
  // A local const, not the outer `map` variable, for the calls below: other
  // closures in this file (onBeforeUnmount, the watchers) can reassign the
  // outer `map` asynchronously, so TypeScript can't narrow it past null
  // through them — this instance is exactly what was just constructed.
  const instance = new maplibregl.Map({
    container: container.value,
    style: await buildStyle(theme),
    center: [4.35, 50.85],
    zoom: 7,
    attributionControl: { compact: true },
  })
  map = instance
  instance.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right')

  instance.on('load', () => {
    addRouteLayers()
    fitToRoutes(maplibregl)
  })
}

onMounted(init)

onBeforeUnmount(() => {
  map?.remove()
  map = null
})

watch(
  () => props.routes,
  async () => {
    if (!map) return
    const source = map.getSource(SOURCE_ID) as import('maplibre-gl').GeoJSONSource | undefined
    if (!source) return
    source.setData(toFeatureCollection(props.routes))
    const maplibregl = await import('maplibre-gl')
    fitToRoutes(maplibregl)
  },
  { deep: true },
)

watch(
  () => props.selectedSlug,
  (slug) => {
    if (!map?.getLayer(LINE_LAYER_ID)) return
    map.setPaintProperty(LINE_LAYER_ID, 'line-width', [
      'case',
      ['==', ['get', 'slug'], slug ?? ''],
      5,
      3,
    ])
    map.setPaintProperty(LINE_LAYER_ID, 'line-opacity', [
      'case',
      ['==', ['get', 'slug'], slug ?? ''],
      1,
      0.75,
    ])
  },
)

watch(resolved, async (theme) => {
  if (!map) return
  const style = await buildStyle(theme === 'dark' ? 'dark' : 'light')
  map.once('style.load', addRouteLayers)
  map.setStyle(style)
})
</script>

<template>
  <div ref="container" class="size-full" />
</template>
