<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { ExternalLink, FolderInput, Pause } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { libraryScan } from '@/utils/accountStorage'
import { openAlbumInGooglePhotos } from '@/utils/googlePhotos'
import {
  cancelTakeaway,
  loadTakeawayPlan,
  setAlbumSize,
  startTakeaway,
  takeaway,
} from '@/utils/takeaway'

const SIZES = [500, 1000, 2000, 5000]

const plan = computed(() => takeaway.plan)
const libraryBusy = computed(() => libraryScan.startedAt > 0 || libraryScan.repairing)
const albums = computed(() => takeaway.result?.Albums ?? [])
const fraction = computed(() => {
  const progress = takeaway.progress
  return progress && progress.Total > 0 ? progress.Done / progress.Total : 0
})

onMounted(() => {
  if (takeaway.available && !takeaway.plan && !takeaway.running) void loadTakeawayPlan()
})
</script>

<template>
  <div
    v-if="takeaway.available"
    class="flex flex-col gap-2 p-3"
  >
    <div class="flex items-start gap-2">
      <FolderInput class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div class="min-w-0 flex-1">
        <p class="text-sm">
          Album takeaway
        </p>
        <p class="text-xs leading-snug text-muted-foreground">
          Groups every photo and video in the selected account into shared albums named “gotohp takeaway 1, 2, …”.
          Open them in Google Photos, share each one, and save it into another account. Nothing is uploaded, moved or
          deleted — switch accounts in the sidebar to choose the source.
        </p>
      </div>
    </div>

    <!-- Size + plan -->
    <div
      v-if="!takeaway.running && !albums.length"
      class="flex flex-col gap-2"
    >
      <div class="flex items-center gap-2 text-xs">
        <span class="shrink-0 text-muted-foreground">Photos per album</span>
        <select
          class="h-7 cursor-pointer rounded-md border bg-background px-1.5 text-xs"
          :value="takeaway.albumSize"
          aria-label="Photos per album"
          @change="setAlbumSize(Number(($event.target as HTMLSelectElement).value)); loadTakeawayPlan()"
        >
          <option
            v-for="size in SIZES"
            :key="size"
            :value="size"
          >
            {{ size.toLocaleString() }}
          </option>
        </select>
        <span class="text-muted-foreground">smaller albums are quicker to save on a phone</span>
      </div>
      <p
        v-if="plan && plan.Items"
        class="text-xs text-muted-foreground tabular-nums"
      >
        {{ plan.Items.toLocaleString() }} items → <span class="text-foreground">{{ plan.Albums }} album(s)</span>
        <template v-if="plan.Skipped">
          · {{ plan.Skipped.toLocaleString() }} skipped (bin, shared, duplicates)
        </template>
      </p>
      <p
        v-else-if="plan && !plan.Items"
        class="text-xs text-muted-foreground"
      >
        Nothing in this account to group.
      </p>
      <div class="flex items-center gap-2">
        <Button
          size="sm"
          class="cursor-pointer"
          :disabled="takeaway.planning || libraryBusy"
          variant="outline"
          @click="loadTakeawayPlan"
        >
          {{ takeaway.planning ? 'Reading…' : plan ? 'Refresh' : 'Check' }}
        </Button>
        <Button
          v-if="plan && plan.Items"
          size="sm"
          class="cursor-pointer"
          :disabled="libraryBusy"
          @click="startTakeaway"
        >
          Create {{ plan.Albums }} album(s)
        </Button>
      </div>
    </div>

    <!-- Running -->
    <div
      v-if="takeaway.running"
      class="rounded-lg border border-primary/30 bg-primary/5 p-3 text-xs"
    >
      <div class="flex items-center gap-2">
        <span class="min-w-0 flex-1 truncate font-medium">
          {{ takeaway.stopping ? 'Stopping…' : takeaway.progress ? `Filling ${takeaway.progress.AlbumTitle}` : 'Starting…' }}
        </span>
        <span
          v-if="takeaway.progress && takeaway.progress.Total"
          class="shrink-0 text-muted-foreground tabular-nums"
        >{{ takeaway.progress.Done.toLocaleString() }} / {{ takeaway.progress.Total.toLocaleString() }}</span>
      </div>
      <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-primary/15">
        <div
          class="h-full rounded-full bg-primary transition-[width] duration-500"
          :style="{ width: `${fraction * 100}%` }"
        />
      </div>
      <Button
        variant="outline"
        size="sm"
        class="mt-2.5 w-full cursor-pointer"
        :disabled="takeaway.stopping"
        @click="cancelTakeaway"
      >
        <Pause class="size-3.5" />
        Stop after this batch
      </Button>
    </div>

    <!-- Result: the albums to open on the phone -->
    <div
      v-if="albums.length && !takeaway.running"
      class="rounded-lg border p-3 text-xs"
    >
      <p class="leading-snug">
        <span class="font-medium">{{ albums.length }} album(s) ready</span> with {{ takeaway.result?.Items.toLocaleString() }} items.
        Open each one in Google Photos, share it, then open the link in the other account and Save all. Delete these
        albums afterwards — they only group the photos.
        <span
          v-if="takeaway.result?.Stopped"
          class="text-amber-300"
        >Stopped early; run again to group the rest.</span>
      </p>
      <ul class="mt-2 max-h-56 divide-y overflow-y-auto rounded-lg border">
        <li
          v-for="album in albums"
          :key="album.AlbumKey"
          class="flex items-center gap-2 px-2.5 py-1.5"
        >
          <span class="min-w-0 flex-1 truncate">{{ album.Title }}</span>
          <span class="shrink-0 text-muted-foreground tabular-nums">{{ album.Count.toLocaleString() }}</span>
          <button
            type="button"
            class="flex size-6 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
            title="Open in Google Photos"
            :aria-label="`Open ${album.Title} in Google Photos`"
            @click="openAlbumInGooglePhotos(album.AlbumKey)"
          >
            <ExternalLink class="size-3.5" />
          </button>
        </li>
      </ul>
    </div>
  </div>
</template>
