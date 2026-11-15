<script setup lang="ts">
import { computed, ref } from 'vue'
import { Download, Sparkles, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import NotesSheet from '@/components/NotesSheet.vue'
import { appUpdate, dismissUpdate, openExternal, releasePageUrl, updateBannerVisible } from '@/utils/appUpdate'

const notesOpen = ref(false)

const update = computed(() => appUpdate.update)
const published = computed(() =>
  update.value?.PublishedAt ? new Date(update.value.PublishedAt).toLocaleDateString() : '',
)
const description = computed(() => {
  const parts = [`You have ${update.value?.Current || 'an older version'}.`]
  if (published.value) parts.push(`Released ${published.value}.`)
  return parts.join(' ')
})
</script>

<template>
  <div
    v-if="updateBannerVisible && update"
    class="flex h-9 shrink-0 items-center gap-2 border-b border-primary/25 bg-primary/10 px-3 text-xs select-none"
    style="--wails-draggable: none"
    role="status"
  >
    <Sparkles class="size-3.5 shrink-0 text-primary" />
    <span class="min-w-0 truncate">
      <span class="font-medium">gotohp {{ update.Latest }} is available</span>
      <span class="text-muted-foreground"> · you have {{ update.Current || 'an older version' }}</span>
    </span>
    <div class="ml-auto flex shrink-0 items-center gap-1">
      <button
        type="button"
        class="cursor-pointer rounded px-1.5 py-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
        @click="notesOpen = true"
      >
        What's new
      </button>
      <button
        type="button"
        class="flex cursor-pointer items-center gap-1 rounded bg-primary px-2 py-0.5 font-medium text-primary-foreground hover:bg-primary/90"
        title="Open the release page to download it"
        @click="openExternal(releasePageUrl())"
      >
        <Download class="size-3" />
        Download
      </button>
      <button
        type="button"
        class="flex cursor-pointer items-center rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        title="Hide until the next version"
        aria-label="Hide this update notice"
        @click="dismissUpdate"
      >
        <X class="size-3.5" />
      </button>
    </div>

    <NotesSheet
      v-model:open="notesOpen"
      :title="update.Name || `gotohp ${update.Latest}`"
      :description="description"
      :source="update.Notes"
      empty-text="This release has no notes."
    >
      <template #footer>
        <Button
          size="sm"
          class="mt-4 w-full cursor-pointer"
          @click="openExternal(releasePageUrl())"
        >
          <Download />
          Download {{ update.Latest }} from GitHub
        </Button>
      </template>
    </NotesSheet>
  </div>
</template>
