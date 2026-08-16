<script setup lang="ts">
import { computed, ref } from 'vue'
import { useToast } from '@nuxt/ui/composables'
import { api } from '@/api/client'
import type { Account } from '@/api/types'

const props = defineProps<{ accounts: Account[] }>()
const emit = defineEmits<{ uploaded: [] }>()

const toast = useToast()

const file = ref<File | null>(null)
const name = ref('')
const description = ref('')
const tags = ref('')
const selectedTargets = ref<string[]>([])

const busy = ref(false)

const targetOptions = computed(() =>
  props.accounts.map((a) => ({ label: a.label || a.id, value: a.id })),
)

const canSubmit = computed(() => !!file.value && !busy.value)

// Offer the filename as a title so the common case is a single click.
function onFileChange(next: File | null) {
  file.value = next
  if (next && !name.value) {
    name.value = next.name.replace(/\.gpx$/i, '').replace(/[-_]+/g, ' ')
  }
}

function reset() {
  file.value = null
  name.value = ''
  description.value = ''
  tags.value = ''
  selectedTargets.value = []
}

async function submit() {
  if (!file.value) return
  busy.value = true
  try {
    const created = await api.upload({
      file: file.value,
      name: name.value.trim(),
      description: description.value.trim(),
      tags: tags.value.trim(),
      // Empty means "use the library default targets".
      targets: selectedTargets.value.join(','),
      // Ownership always comes from the session — the server ignores this
      // field when one exists, so there is nothing for the uploader to set.
    })
    toast.add({
      title: 'Route added',
      description: `“${created.name}” is in the library.`,
      icon: 'i-lucide-check',
      color: 'success',
    })
    reset()
    emit('uploaded')
  } catch (err) {
    toast.add({
      title: 'Upload failed',
      description: err instanceof Error ? err.message : String(err),
      icon: 'i-lucide-triangle-alert',
      color: 'error',
    })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UCard variant="outline">
    <template #header>
      <h2 class="font-medium text-highlighted">Add a route</h2>
    </template>

    <div class="flex flex-col gap-4">
      <UFileUpload
        :model-value="file"
        accept=".gpx,application/gpx+xml"
        icon="i-lucide-route"
        label="Drop a .gpx here"
        description="or click to choose one"
        class="min-h-36"
        @update:model-value="onFileChange($event as File | null)"
      />

      <div class="grid gap-3">
        <UFormField label="Name">
          <UInput v-model="name" placeholder="Kemmelberg Loop" class="w-full" />
        </UFormField>
        <UFormField label="Description">
          <UInput v-model="description" placeholder="Optional" class="w-full" />
        </UFormField>
        <UFormField label="Tags" hint="comma separated">
          <UInput v-model="tags" placeholder="gravel, hills" class="w-full" />
        </UFormField>
      </div>

      <UFormField
        v-if="targetOptions.length"
        label="Send to"
        help="Leave all unticked to use the library's default targets."
      >
        <UCheckboxGroup v-model="selectedTargets" :items="targetOptions" orientation="horizontal" />
      </UFormField>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton v-if="file" color="neutral" variant="ghost" :disabled="busy" @click="reset">
          Clear
        </UButton>
        <UButton icon="i-lucide-upload" :loading="busy" :disabled="!canSubmit" @click="submit">
          Upload route
        </UButton>
      </div>
    </template>
  </UCard>
</template>
