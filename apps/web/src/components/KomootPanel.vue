<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { KomootTour } from '@/api/types'

const emit = defineEmits<{ imported: [] }>()

const tours = ref<KomootTour[]>([])
const selected = ref<string[]>([])
const loading = ref(true)
const importing = ref(false)
const error = ref('')
const notice = ref('')
/** Komoot is optional; when it is not configured we hide rather than nag. */
const available = ref(true)

const importable = computed(() => tours.value.filter((t) => !t.imported))
const canImport = computed(() => selected.value.length > 0 && !importing.value)

async function load() {
  loading.value = true
  error.value = ''
  try {
    tours.value = await api.komootTours()
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err)
    // 501 means "not configured" — that is not an error worth shouting about.
    if (message.includes('not configured')) {
      available.value = false
    } else {
      error.value = message
    }
  } finally {
    loading.value = false
  }
}

function toggleAll() {
  selected.value =
    selected.value.length === importable.value.length ? [] : importable.value.map((t) => t.id)
}

async function runImport() {
  importing.value = true
  error.value = ''
  notice.value = ''
  try {
    const result = await api.komootImport(selected.value)
    const skipped = Object.entries(result.skipped)
    notice.value = `Imported ${result.imported.length} route${
      result.imported.length === 1 ? '' : 's'
    }${skipped.length ? `, skipped ${skipped.length}` : ''}.`
    if (skipped.length) {
      // Say *why* each was skipped; "skipped 3" alone is useless.
      error.value = skipped.map(([id, reason]) => `${id}: ${reason}`).join(' · ')
    }
    selected.value = []
    await load()
    emit('imported')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    importing.value = false
  }
}

onMounted(load)
</script>

<template>
  <section v-if="available" class="panel">
    <header>
      <div>
        <h2>Import from Komoot</h2>
        <p class="summary">
          <template v-if="loading">Loading tours…</template>
          <template v-else-if="!tours.length">No planned routes in that account.</template>
          <template v-else>
            {{ importable.length }} of {{ tours.length }} not imported yet
          </template>
        </p>
      </div>
      <div class="actions">
        <button type="button" class="ghost" :disabled="loading" @click="load">Refresh</button>
        <button
          v-if="importable.length"
          type="button"
          class="ghost"
          @click="toggleAll"
        >
          {{ selected.length === importable.length ? 'Select none' : 'Select all' }}
        </button>
        <button type="button" class="primary" :disabled="!canImport" @click="runImport">
          {{ importing ? 'Importing…' : `Import ${selected.length || ''}`.trim() }}
        </button>
      </div>
    </header>

    <p v-if="notice" class="notice">{{ notice }}</p>
    <p v-if="error" class="error">{{ error }}</p>

    <ul v-if="tours.length" class="tours">
      <li v-for="tour in tours" :key="tour.id" :class="{ done: tour.imported }">
        <label>
          <input
            v-model="selected"
            type="checkbox"
            :value="tour.id"
            :disabled="tour.imported"
          />
          <span class="name">{{ tour.name }}</span>
        </label>
        <span class="meta">
          {{ (tour.distanceM / 1000).toFixed(1) }} km · {{ Math.round(tour.ascentM) }} m
          <span v-if="tour.sport"> · {{ tour.sport }}</span>
        </span>
        <span v-if="tour.imported" class="tag">imported</span>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.panel {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  padding: 1rem 1.15rem 1.15rem;
}

header {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: center;
  justify-content: space-between;
}

h2 {
  margin: 0;
  font-size: 1rem;
}

.summary {
  margin: 0.2rem 0 0;
  font-size: 0.85rem;
  color: var(--text-muted);
}

.actions {
  display: flex;
  gap: 0.5rem;
}

button {
  font: inherit;
  font-size: 0.85rem;
  padding: 0.4rem 0.85rem;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface-sunken);
  color: var(--text);
  cursor: pointer;
}

button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

button.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--on-accent);
}

.tours {
  list-style: none;
  margin: 0.9rem 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.tours li {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.4rem 0.2rem;
  border-top: 1px solid var(--border);
  font-size: 0.88rem;
}

.tours li.done {
  color: var(--text-muted);
}

label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex: 1;
  min-width: 0;
  cursor: pointer;
}

.name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.meta {
  font-size: 0.78rem;
  color: var(--text-muted);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.tag {
  font-size: 0.7rem;
  padding: 0.1rem 0.4rem;
  border-radius: 4px;
  background: var(--surface-sunken);
  color: var(--ok);
}

.notice {
  margin: 0.8rem 0 0;
  font-size: 0.85rem;
  color: var(--ok);
}

.error {
  margin: 0.5rem 0 0;
  font-size: 0.83rem;
  color: var(--danger);
}
</style>
