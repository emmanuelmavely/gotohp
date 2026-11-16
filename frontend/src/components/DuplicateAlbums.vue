<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ExternalLink, Images } from '@lucide/vue'
import * as guiBindings from '../../bindings/app/internal/gui'
import { albumStore } from '@/utils/albums'
import { openAlbumInGooglePhotos } from '@/utils/googlePhotos'

interface DuplicateGroup {
  Title: string
  Albums: { MediaKey: string, Title: string, ItemCount: number }[] | null
  Items: number
}

// Reached by name so older bindings still build.
const api = (guiBindings as unknown as {
  LibraryService?: { DuplicateAlbums?: () => Promise<DuplicateGroup[] | null> }
}).LibraryService

const groups = ref<DuplicateGroup[]>([])
const open = ref(false)
const extra = computed(() => groups.value.reduce((sum, group) => sum + (group.Albums?.length ?? 1) - 1, 0))

async function load() {
  try {
    groups.value = (await api?.DuplicateAlbums?.()) ?? []
  } catch {
    groups.value = []
  }
}
onMounted(load)
// The list is read from the last library read; follow it.
watch(() => albumStore.syncedAt, load)
</script>

<template>
  <div
    v-if="api?.DuplicateAlbums"
    class="flex flex-col gap-2 p-3"
  >
    <div class="flex items-start gap-2">
      <Images class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div class="min-w-0 flex-1">
        <p class="text-sm">
          Albums with the same name
        </p>
        <p class="text-xs leading-snug text-muted-foreground">
          <template v-if="groups.length">
            {{ groups.length }} name(s) are used by more than one album ({{ extra }} extra). Google Photos has no way for
            this app to move photos between albums, so open them and merge them there.
          </template>
          <template v-else>
            None found in the last library read.
          </template>
        </p>
      </div>
      <button
        v-if="groups.length"
        type="button"
        class="shrink-0 cursor-pointer text-xs text-primary hover:underline"
        @click="open = !open"
      >
        {{ open ? 'Hide' : 'Show' }}
      </button>
    </div>
    <ul
      v-if="open && groups.length"
      class="max-h-72 divide-y overflow-y-auto rounded-lg border text-xs"
    >
      <li
        v-for="group in groups"
        :key="group.Title"
        class="px-2.5 py-2"
      >
        <p class="font-medium">
          {{ group.Title }}
          <span class="font-normal text-muted-foreground tabular-nums">· {{ group.Albums?.length }} albums, {{ group.Items.toLocaleString() }} items</span>
        </p>
        <div class="mt-1 flex flex-wrap gap-1.5">
          <button
            v-for="album in group.Albums ?? []"
            :key="album.MediaKey"
            type="button"
            class="flex cursor-pointer items-center gap-1 rounded border px-1.5 py-0.5 text-muted-foreground tabular-nums hover:bg-accent hover:text-foreground"
            :title="`Open this ${album.ItemCount}-item album in Google Photos`"
            @click="openAlbumInGooglePhotos(album.MediaKey)"
          >
            {{ album.ItemCount.toLocaleString() }} items
            <ExternalLink class="size-3" />
          </button>
        </div>
      </li>
    </ul>
  </div>
</template>
