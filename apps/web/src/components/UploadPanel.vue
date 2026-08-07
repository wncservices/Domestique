<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { Account } from '@/api/types'

const props = defineProps<{ accounts: Account[] }>()
const emit = defineEmits<{ uploaded: [] }>()

const file = ref<File | null>(null)
const name = ref('')
const description = ref('')
const tags = ref('')
const selectedTargets = ref<string[]>([])
const uploadedBy = ref(localStorage.getItem('domestique.rider') ?? '')

const busy = ref(false)
const error = ref('')
const dragging = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

const canSubmit = computed(() => !!file.value && !busy.value)

function pick(files: FileList | null) {
  const chosen = files?.[0]
  if (!chosen) return
  if (!chosen.name.toLowerCase().endsWith('.gpx')) {
    error.value = 'That is not a .gpx file.'
    return
  }
  error.value = ''
  file.value = chosen
  // Offer the filename as a title so the common case is a single click.
  if (!name.value) {
    name.value = chosen.name.replace(/\.gpx$/i, '').replace(/[-_]+/g, ' ')
  }
}

function onDrop(event: DragEvent) {
  dragging.value = false
  pick(event.dataTransfer?.files ?? null)
}

function reset() {
  file.value = null
  name.value = ''
  description.value = ''
  tags.value = ''
  selectedTargets.value = []
  if (fileInput.value) fileInput.value.value = ''
}

async function submit() {
  if (!file.value) return
  busy.value = true
  error.value = ''
  try {
    await api.upload({
      file: file.value,
      name: name.value.trim(),
      description: description.value.trim(),
      tags: tags.value.trim(),
      // Empty means "use the library default targets".
      targets: selectedTargets.value.join(','),
      uploadedBy: uploadedBy.value.trim(),
    })
    // Remember who is uploading; it is the same person most of the time.
    localStorage.setItem('domestique.rider', uploadedBy.value.trim())
    reset()
    emit('uploaded')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="panel">
    <h2>Add a route</h2>

    <div
      class="dropzone"
      :class="{ dragging, filled: !!file }"
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
      @click="fileInput?.click()"
    >
      <input
        ref="fileInput"
        type="file"
        accept=".gpx,application/gpx+xml"
        hidden
        @change="pick(($event.target as HTMLInputElement).files)"
      />
      <p v-if="file" class="filename">{{ file.name }}</p>
      <p v-else>Drop a <code>.gpx</code> here, or click to choose one</p>
    </div>

    <div class="fields">
      <label>
        <span>Name</span>
        <input v-model="name" type="text" placeholder="Kemmelberg Loop" />
      </label>
      <label>
        <span>Uploaded by</span>
        <input v-model="uploadedBy" type="text" placeholder="wilant" />
      </label>
      <label class="wide">
        <span>Description</span>
        <input v-model="description" type="text" placeholder="Optional" />
      </label>
      <label class="wide">
        <span>Tags</span>
        <input v-model="tags" type="text" placeholder="gravel, hills (comma separated)" />
      </label>
    </div>

    <fieldset v-if="props.accounts.length">
      <legend>Send to</legend>
      <label v-for="account in props.accounts" :key="account.id" class="check">
        <input v-model="selectedTargets" type="checkbox" :value="account.id" />
        {{ account.label || account.id }}
      </label>
      <p class="hint">Leave all unticked to use the library's default targets.</p>
    </fieldset>

    <p v-if="error" class="error">{{ error }}</p>

    <div class="actions">
      <button v-if="file" type="button" class="ghost" :disabled="busy" @click="reset">
        Clear
      </button>
      <button type="button" class="primary" :disabled="!canSubmit" @click="submit">
        {{ busy ? 'Uploading…' : 'Upload route' }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.panel {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  padding: 1rem 1.15rem 1.15rem;
}

h2 {
  margin: 0 0 0.85rem;
  font-size: 1rem;
}

.dropzone {
  border: 1.5px dashed var(--border);
  border-radius: 10px;
  padding: 1.4rem 1rem;
  text-align: center;
  cursor: pointer;
  color: var(--text-muted);
  font-size: 0.9rem;
  transition: border-color 0.15s, background 0.15s;
}

.dropzone:hover,
.dropzone.dragging {
  border-color: var(--accent);
  background: color-mix(in srgb, var(--accent) 7%, transparent);
}

.dropzone.filled {
  border-style: solid;
  border-color: color-mix(in srgb, var(--ok) 45%, transparent);
}

.dropzone p {
  margin: 0;
}

.filename {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.85rem;
  color: var(--text);
}

.fields {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 0.7rem;
  margin-top: 0.9rem;
}

label {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  font-size: 0.78rem;
  color: var(--text-muted);
}

label.wide {
  grid-column: 1 / -1;
}

input[type='text'] {
  font: inherit;
  font-size: 0.88rem;
  padding: 0.4rem 0.6rem;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface-sunken);
  color: var(--text);
}

fieldset {
  margin: 0.9rem 0 0;
  padding: 0.7rem 0.85rem;
  border: 1px solid var(--border);
  border-radius: 8px;
}

legend {
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
  padding: 0 0.35rem;
}

.check {
  flex-direction: row;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.88rem;
  color: var(--text);
  margin-right: 1rem;
  display: inline-flex;
}

.hint {
  margin: 0.5rem 0 0;
  font-size: 0.75rem;
  color: var(--text-muted);
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  margin-top: 0.9rem;
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

.error {
  margin: 0.8rem 0 0;
  font-size: 0.83rem;
  color: var(--danger);
}
</style>
