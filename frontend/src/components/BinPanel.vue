<script setup lang="ts">
import { computed, ref } from 'vue'
import { toast } from 'vue-sonner'
import { ExternalLink, Film, Image, Trash2 } from '@lucide/vue'
import { LibraryService } from '../../bindings/app/internal/gui'
import { Button } from '@/components/ui/button'
import LibraryScanLive from '@/components/LibraryScanLive.vue'
import { beginLibraryScan, endLibraryScan, libraryScan, minuteClock } from '@/utils/accountStorage'
import { debugLog } from '@/utils/debug'
import { formatBytes, formatTimeAgo } from '@/utils/format'
import { openInGooglePhotos } from '@/utils/googlePhotos'

/** One item in the library's bin, as the backend reports it. */
interface BinItem {
  MediaKey: string
  FileName: string
  IsVideo: boolean
  SizeBytes: number
  /** Unix milliseconds; 0 when unknown. */
  TrashedAt: number
  DeletesAt: number
  /** The still this video was joined to, when the Live Photo repair binned it. */
  RepairedPhoto: string
  RepairedPhotoMediaKey: string
  /** No record of the repair exists, but the video looks like a repaired orphan. */
  RepairGuessed?: boolean
  /** Another item with the same content is still in the library. */
  CopyInLibrary: boolean
  Shared: boolean
}

interface BinReport {
  Items: BinItem[] | null
  TotalBytes: number
  FromRepair: number
}

// Bin() is new; reach it without depending on regenerated bindings.
const fetchBin = (LibraryService as unknown as { Bin?: () => Promise<BinReport> }).Bin

const DAY_MS = 24 * 60 * 60 * 1000

const report = ref<BinReport | null>(null)
const isLoading = ref(false)
const filter = ref<'all' | 'repair' | 'other'>('all')

const isLibraryBusy = computed(() => libraryScan.startedAt > 0 || libraryScan.repairing)
const items = computed<BinItem[]>(() => report.value?.Items ?? [])
const shown = computed(() =>
  items.value.filter((item) => {
    if (filter.value === 'repair') return item.RepairedPhoto !== ''
    if (filter.value === 'other') return item.RepairedPhoto === ''
    return true
  }),
)

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

async function loadBin() {
  if (isLibraryBusy.value || !fetchBin) return
  isLoading.value = true
  beginLibraryScan()
  try {
    report.value = await fetchBin()
  } catch (error) {
    debugLog('error', 'frontend', 'Bin failed', errorText(error))
    toast.error('Could not read the bin', { description: errorText(error) })
  } finally {
    endLibraryScan()
    isLoading.value = false
  }
}

function daysLeft(item: BinItem): string {
  if (!item.DeletesAt) return ''
  const days = Math.ceil((item.DeletesAt - minuteClock.value) / DAY_MS)
  if (days <= 0) return 'deleted soon'
  return days === 1 ? '1 day left' : `${days} days left`
}
</script>

<template>
  <section class="flex flex-col gap-1.5">
    <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
      Bin
    </h3>
    <div class="rounded-xl border bg-card p-3">
      <div class="flex items-start gap-3">
        <p class="min-w-0 flex-1 text-xs leading-snug text-muted-foreground">
          What is in your Google Photos bin, and which videos the Live Photo repair put there. Google deletes bin
          items for good after 60 days. Read-only: nothing is restored or deleted from here.
        </p>
        <Button
          variant="outline"
          size="sm"
          class="shrink-0 cursor-pointer"
          :disabled="isLibraryBusy || !fetchBin"
          @click="loadBin"
        >
          <Trash2 :class="isLoading && 'animate-pulse'" />
          {{ isLoading ? 'Checking…' : report ? 'Check again' : 'Check bin' }}
        </Button>
      </div>

      <LibraryScanLive
        v-if="isLoading"
        class="mt-3"
      />

      <template v-else-if="report">
        <p
          v-if="items.length === 0"
          class="mt-3 border-t pt-3 text-xs text-muted-foreground"
        >
          The bin is empty.
        </p>
        <template v-else>
          <div class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 border-t pt-3 text-xs">
            <span class="font-medium tabular-nums">
              {{ items.length.toLocaleString() }} item(s) · {{ formatBytes(report.TotalBytes, 2) }}
            </span>
            <div
              class="ml-auto flex rounded-md border p-0.5 text-[11px]"
              role="radiogroup"
              aria-label="Show"
            >
              <button
                v-for="option in ([
                  ['all', 'All'],
                  ['repair', `From repair (${report.FromRepair})`],
                  ['other', 'Other'],
                ] as const)"
                :key="option[0]"
                type="button"
                role="radio"
                :aria-checked="filter === option[0]"
                :class="[
                  'cursor-pointer rounded px-2 py-0.5',
                  filter === option[0] ? 'bg-accent text-foreground' : 'text-muted-foreground hover:text-foreground',
                ]"
                @click="filter = option[0]"
              >
                {{ option[1] }}
              </button>
            </div>
          </div>

          <p
            v-if="shown.length === 0"
            class="mt-2 text-xs text-muted-foreground"
          >
            Nothing here for this filter.
          </p>
          <ul
            v-else
            class="mt-2 max-h-72 divide-y overflow-y-auto rounded-lg border"
          >
            <li
              v-for="item in shown"
              :key="item.MediaKey"
              class="flex gap-2 px-2.5 py-1.5"
            >
              <component
                :is="item.IsVideo ? Film : Image"
                class="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
              />
              <div class="min-w-0 flex-1 text-xs leading-snug">
                <p class="flex items-center gap-1.5">
                  <span class="min-w-0 truncate">
                    {{ item.FileName || item.MediaKey }}
                    <span class="text-muted-foreground tabular-nums">· {{ formatBytes(item.SizeBytes) }}</span>
                  </span>
                  <span
                    v-if="item.TrashedAt"
                    class="ml-auto shrink-0 text-[11px] text-muted-foreground tabular-nums"
                    :title="new Date(item.TrashedAt).toLocaleString()"
                  >
                    {{ formatTimeAgo(item.TrashedAt, minuteClock) }} · {{ daysLeft(item) }}
                  </span>
                </p>
                <p
                  v-if="item.RepairedPhoto"
                  class="mt-0.5 flex items-center gap-1.5 text-primary"
                >
                  <span
                    class="min-w-0 truncate"
                    :title="item.RepairGuessed ? 'No record of this repair was kept (it ran before the app started keeping one); the match is by name and content.' : undefined"
                  >{{ item.RepairGuessed ? 'Probably Live Photo repair' : 'Live Photo repair' }} · now the motion of {{ item.RepairedPhoto }}</span>
                  <button
                    v-if="item.RepairedPhotoMediaKey"
                    type="button"
                    class="ml-auto flex shrink-0 cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-accent hover:text-foreground"
                    title="Open the photo in Google Photos"
                    @click="openInGooglePhotos(item.RepairedPhotoMediaKey)"
                  >
                    <ExternalLink class="size-3" />
                    Open
                  </button>
                </p>
                <p
                  v-if="item.CopyInLibrary || item.Shared"
                  class="mt-0.5 text-[11px] text-muted-foreground"
                >
                  <template v-if="item.CopyInLibrary">
                    A copy with the same content is still in your library.
                  </template>
                  <template v-if="item.Shared">
                    From a shared album or partner.
                  </template>
                </p>
              </div>
            </li>
          </ul>
        </template>
      </template>
    </div>
  </section>
</template>
