<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Check, FolderPlus, Images, RefreshCw, X } from '@lucide/vue'
import { Input } from '@/components/ui/input'
import { albumStore, recentAlbumKeys, refreshAlbums, type Album } from '@/utils/albums'
import { debugEnabled } from '@/utils/debug'
import { formatTimeAgo } from '@/utils/format'

/** The album name to create, or the key (AF1Qip…) of an existing album. */
const model = defineModel<string>({ required: true })
const emit = defineEmits<{ submit: [] }>()

const query = ref('')
const selected = ref<Album | null>(null)

const MAX_SHOWN = 200

const albums = computed(() => albumStore.albums)
const recent = computed(() =>
  recentAlbumKeys.keys
    .map(key => albums.value.find(album => album.MediaKey === key))
    .filter((album): album is Album => Boolean(album)),
)
const matches = computed(() => {
  const text = query.value.trim().toLowerCase()
  if (text) return albums.value.filter(album => album.Title.toLowerCase().includes(text)).slice(0, MAX_SHOWN)
  // Recently used albums first, then the rest by last change.
  const first = new Set(recent.value.map(album => album.MediaKey))
  return [...recent.value, ...albums.value.filter(album => !first.has(album.MediaKey))].slice(0, MAX_SHOWN)
})
const recentCount = computed(() => (query.value.trim() ? 0 : recent.value.length))
const exactMatch = computed(() => {
  const text = query.value.trim().toLowerCase()
  return text ? albums.value.find(album => album.Title.trim().toLowerCase() === text) ?? null : null
})
const newName = computed(() => (exactMatch.value ? '' : query.value.trim()))

onMounted(() => {
  // The list is already there from the start-up fetch; pick up recent
  // changes quietly.
  void refreshAlbums()
  if (model.value.startsWith('AF1Qip')) {
    selected.value = albums.value.find(album => album.MediaKey === model.value) ?? null
  }
})

function choose(album: Album) {
  selected.value = album
  model.value = album.MediaKey
}

function chooseNew() {
  selected.value = null
  model.value = newName.value
}

function onType(value: string | number) {
  query.value = String(value)
  const existing = exactMatch.value
  selected.value = existing
  // A typed name that matches an album exactly goes into that album;
  // anything else is a new album name (or a pasted key) until one is picked.
  model.value = existing ? existing.MediaKey : query.value.trim()
}

function clearSelection() {
  selected.value = null
  model.value = query.value.trim()
}

function onEnter() {
  if (!model.value.trim()) {
    if (matches.value.length === 1) choose(matches.value[0]!)
    else return
  }
  emit('submit')
}
</script>

<template>
  <div class="flex flex-col gap-2">
    <!-- What the upload will go into -->
    <div
      v-if="selected"
      class="flex items-center gap-2 rounded-lg border border-primary/40 bg-primary/10 px-2.5 py-2 text-sm"
    >
      <Images class="size-4 shrink-0 text-primary" />
      <span class="min-w-0 flex-1 truncate">
        Add to <span class="font-medium">{{ selected.Title }}</span>
        <span class="text-xs text-muted-foreground tabular-nums"> · {{ selected.ItemCount.toLocaleString() }} items</span>
      </span>
      <button
        type="button"
        class="flex shrink-0 cursor-pointer items-center rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
        aria-label="Choose another album"
        @click="clearSelection"
      >
        <X class="size-3.5" />
      </button>
    </div>
    <div
      v-else-if="model.trim()"
      class="flex items-center gap-2 rounded-lg border px-2.5 py-2 text-sm"
    >
      <FolderPlus class="size-4 shrink-0 text-muted-foreground" />
      <span class="min-w-0 flex-1 truncate">
        {{ model.startsWith('AF1Qip') ? 'Add to album' : 'Create new album' }} <span class="font-medium">{{ model.trim() }}</span>
      </span>
    </div>

    <Input
      id="album-input"
      :model-value="query"
      placeholder="Search your albums or type a new name"
      autofocus
      autocomplete="off"
      @update:model-value="onType"
      @keydown.enter="onEnter"
    />

    <ul
      v-if="albums.length"
      class="max-h-56 divide-y overflow-y-auto rounded-lg border text-sm"
      role="listbox"
      aria-label="Your albums"
    >
      <li v-if="newName">
        <button
          type="button"
          class="flex w-full cursor-pointer items-center gap-2 px-2.5 py-1.5 text-left hover:bg-accent/50"
          @click="chooseNew"
        >
          <FolderPlus class="size-3.5 shrink-0 text-primary" />
          <span class="min-w-0 flex-1 truncate">Create new album “{{ newName }}”</span>
        </button>
      </li>
      <li
        v-for="(album, index) in matches"
        :key="album.MediaKey"
      >
        <p
          v-if="recentCount && (index === 0 || index === recentCount)"
          class="bg-muted/40 px-2.5 py-1 text-[10px] font-semibold tracking-wider text-muted-foreground uppercase"
        >
          {{ index === 0 ? 'Recent' : 'All albums' }}
        </p>
        <button
          type="button"
          role="option"
          :aria-selected="selected?.MediaKey === album.MediaKey"
          class="flex w-full cursor-pointer items-center gap-2 px-2.5 py-1.5 text-left hover:bg-accent/50"
          :class="selected?.MediaKey === album.MediaKey && 'bg-primary/10'"
          @click="choose(album)"
        >
          <Images class="size-3.5 shrink-0 text-muted-foreground" />
          <span class="min-w-0 flex-1 truncate">{{ album.Title }}</span>
          <span class="shrink-0 text-xs text-muted-foreground tabular-nums">{{ album.ItemCount.toLocaleString() }}</span>
          <code
            v-if="debugEnabled"
            class="shrink-0 rounded bg-muted px-1 font-mono text-[10px] text-muted-foreground"
          >type {{ album.Type }}</code>
          <Check
            v-if="selected?.MediaKey === album.MediaKey"
            class="size-3.5 shrink-0 text-primary"
          />
        </button>
      </li>
      <li
        v-if="!matches.length && !newName"
        class="px-2.5 py-1.5 text-xs text-muted-foreground"
      >
        No album matches.
      </li>
    </ul>

    <p class="flex items-center gap-2 text-[11px] text-muted-foreground">
      <span class="min-w-0 flex-1 tabular-nums">
        <template v-if="albumStore.refreshing && !albumStore.known">
          Fetching your albums from Google Photos… {{ albumStore.itemsRead.toLocaleString() }} items read. This first
          read takes a few minutes; after that the list is ready right away. You can type a new album name meanwhile.
        </template>
        <template v-else>
          {{ albums.length.toLocaleString() }} albums
          <template v-if="albumStore.refreshing"> · checking for changes…</template>
          <template v-else-if="albumStore.syncedAt"> · updated {{ formatTimeAgo(albumStore.syncedAt) }}</template>
        </template>
      </span>
      <button
        v-if="albumStore.available && albumStore.known"
        type="button"
        class="flex shrink-0 cursor-pointer items-center rounded p-1 hover:bg-accent hover:text-foreground disabled:opacity-50"
        title="Check for album changes now"
        aria-label="Refresh albums"
        :disabled="albumStore.refreshing"
        @click="refreshAlbums(true)"
      >
        <RefreshCw
          class="size-3"
          :class="albumStore.refreshing && 'animate-spin'"
        />
      </button>
    </p>
  </div>
</template>
