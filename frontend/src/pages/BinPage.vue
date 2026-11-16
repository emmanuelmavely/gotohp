<script lang="ts">
import { reactive } from 'vue'

/** One item in the library's bin, as the backend reports it. */
export interface BinItem {
  MediaKey: string
  FileName: string
  IsVideo: boolean
  SizeBytes: number
  /** Unix milliseconds; 0 when unknown. */
  TrashedAt: number
  DeletesAt: number
  RepairedPhoto: string
  CopyInLibrary: boolean
  Shared: boolean
}
interface BinReport {
  Items: BinItem[] | null
  TotalBytes: number
}

/** The last bin read, kept while switching pages. */
const binStore = reactive({
  report: null as BinReport | null,
  readAt: 0,
  loading: false,
})
</script>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { toast } from 'vue-sonner'
import { ExternalLink, Film, Image, RefreshCw, RotateCcw, Trash2 } from '@lucide/vue'
import * as guiBindings from '../../bindings/app/internal/gui'
import { Button } from '@/components/ui/button'
import LibraryScanLive from '@/components/LibraryScanLive.vue'
import { beginLibraryScan, endLibraryScan, libraryScan, minuteClock } from '@/utils/accountStorage'
import { debugLog } from '@/utils/debug'
import { formatBytes, formatTimeAgo } from '@/utils/format'
import { openInGooglePhotos } from '@/utils/googlePhotos'

interface BinActionResult {
  Done: number
  Skipped: { FileName: string, Reason: string }[] | null
}
// Reached by name so older bindings still build.
const api = (guiBindings as unknown as {
  LibraryService?: {
    Bin?: () => Promise<BinReport>
    BinRestore?: (mediaKeys: string[]) => Promise<BinActionResult>
    BinDelete?: (mediaKeys: string[], everything: boolean, confirmed: boolean) => Promise<BinActionResult>
  }
}).LibraryService

const DAY_MS = 24 * 60 * 60 * 1000
const MAX_ROWS = 300

const selected = ref(new Set<string>())
const confirming = ref<null | 'selected' | 'all'>(null)
const working = ref(false)

const libraryBusy = computed(() => libraryScan.startedAt > 0 || libraryScan.repairing)
const items = computed<BinItem[]>(() => binStore.report?.Items ?? [])
const rows = computed(() => items.value.slice(0, MAX_ROWS))
const selectedItems = computed(() => items.value.filter(item => selected.value.has(item.MediaKey)))
const selectedBytes = computed(() => selectedItems.value.reduce((sum, item) => sum + item.SizeBytes, 0))
const allSelected = computed(() => items.value.length > 0 && selected.value.size === items.value.length)

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

async function load() {
  if (!api?.Bin || binStore.loading || libraryBusy.value) return
  binStore.loading = true
  beginLibraryScan()
  try {
    binStore.report = await api.Bin()
    binStore.readAt = Date.now()
    const present = new Set(items.value.map(item => item.MediaKey))
    selected.value = new Set([...selected.value].filter(key => present.has(key)))
  } catch (error) {
    debugLog('error', 'frontend', 'Bin failed', errorText(error))
    toast.error('Could not read the bin', { description: errorText(error) })
  } finally {
    endLibraryScan()
    binStore.loading = false
  }
}

onMounted(() => {
  if (!binStore.report) void load()
})

function toggle(key: string) {
  const next = new Set(selected.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  selected.value = next
}

function toggleAll() {
  selected.value = allSelected.value ? new Set() : new Set(items.value.map(item => item.MediaKey))
}

function daysLeft(item: BinItem): string {
  if (!item.DeletesAt) return ''
  const days = Math.ceil((item.DeletesAt - minuteClock.value) / DAY_MS)
  if (days <= 0) return 'deleted soon'
  return days === 1 ? '1 day left' : `${days} days left`
}

function report(action: string, result: BinActionResult) {
  const skipped = result.Skipped ?? []
  const text = `${action} ${result.Done.toLocaleString()} item(s).`
  if (skipped.length === 0) {
    toast.success(text)
    return
  }
  const reasons = [...new Set(skipped.map(item => item.Reason))].join('; ')
  toast.info(text, { description: `${skipped.length} left alone: ${reasons}.`, duration: 10000 })
}

async function restore() {
  if (!api?.BinRestore || selected.value.size === 0) return
  working.value = true
  try {
    report('Restored', await api.BinRestore([...selected.value]))
    selected.value = new Set()
  } catch (error) {
    toast.error('Could not restore', { description: errorText(error) })
  } finally {
    working.value = false
  }
  await load()
}

async function deleteForGood() {
  if (!api?.BinDelete || !confirming.value) return
  const everything = confirming.value === 'all'
  working.value = true
  confirming.value = null
  try {
    report('Deleted', await api.BinDelete(everything ? [] : [...selected.value], everything, true))
    selected.value = new Set()
  } catch (error) {
    toast.error('Could not delete', { description: errorText(error) })
  } finally {
    working.value = false
  }
  await load()
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="rounded-xl border bg-card p-4">
      <LibraryScanLive v-if="binStore.loading && !binStore.report" />
      <template v-else-if="binStore.report">
        <div class="flex items-start gap-3">
          <div class="min-w-0 flex-1">
            <p class="text-3xl leading-none font-semibold tracking-tight tabular-nums">
              {{ items.length.toLocaleString() }} <span class="text-base font-normal text-muted-foreground">items</span>
            </p>
            <p class="mt-1.5 text-xs text-muted-foreground tabular-nums">
              {{ formatBytes(binStore.report.TotalBytes, 2) }}
              <template v-if="binStore.readAt">
                · updated {{ formatTimeAgo(binStore.readAt, minuteClock) }}
              </template>
            </p>
          </div>
          <Button
            variant="ghost"
            size="sm"
            class="shrink-0 cursor-pointer"
            :disabled="binStore.loading || libraryBusy || working"
            @click="load"
          >
            <RefreshCw
              class="size-3.5"
              :class="binStore.loading && 'animate-spin'"
            />
            Refresh
          </Button>
        </div>
        <div
          v-if="items.length"
          class="mt-3 grid grid-cols-3 gap-2"
        >
          <Button
            variant="outline"
            size="sm"
            class="cursor-pointer"
            :disabled="selected.size === 0 || working"
            @click="restore"
          >
            <RotateCcw class="size-3.5" />
            Restore
          </Button>
          <Button
            variant="outline"
            size="sm"
            class="cursor-pointer text-destructive"
            :disabled="selected.size === 0 || working"
            @click="confirming = 'selected'"
          >
            <Trash2 class="size-3.5" />
            Delete
          </Button>
          <Button
            variant="destructive"
            size="sm"
            class="cursor-pointer"
            :disabled="working"
            @click="confirming = 'all'"
          >
            Empty bin
          </Button>
        </div>

        <!-- Confirmation -->
        <div
          v-if="confirming"
          class="mt-3 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm"
        >
          <p class="font-medium">
            <template v-if="confirming === 'all'">
              Delete all {{ items.length.toLocaleString() }} items ({{ formatBytes(binStore.report.TotalBytes, 2) }}) for good?
            </template>
            <template v-else>
              Delete {{ selected.size.toLocaleString() }} items ({{ formatBytes(selectedBytes, 1) }}) for good?
            </template>
          </p>
          <p class="mt-1 text-xs text-muted-foreground">
            This cannot be undone. Items whose content is still in your library, and items from shared albums, are left
            for Google to remove.
          </p>
          <div class="mt-2.5 grid grid-cols-2 gap-2">
            <Button
              variant="outline"
              size="sm"
              class="cursor-pointer"
              @click="confirming = null"
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              class="cursor-pointer"
              @click="deleteForGood"
            >
              Delete for good
            </Button>
          </div>
        </div>
      </template>
      <p
        v-else
        class="text-sm text-muted-foreground"
      >
        The bin could not be read.
        <button
          type="button"
          class="cursor-pointer text-primary hover:underline"
          @click="load"
        >
          Try again
        </button>
      </p>
    </div>

    <template v-if="items.length">
      <div class="flex items-center gap-2 px-1 text-xs">
        <label class="flex cursor-pointer items-center gap-2 select-none">
          <input
            type="checkbox"
            class="accent-primary"
            :checked="allSelected"
            @change="toggleAll"
          >
          Select all
        </label>
        <span
          v-if="selected.size"
          class="ml-auto text-muted-foreground tabular-nums"
        >{{ selected.size.toLocaleString() }} selected · {{ formatBytes(selectedBytes, 1) }}</span>
      </div>
      <ul class="divide-y overflow-hidden rounded-xl border bg-card">
        <li
          v-for="item in rows"
          :key="item.MediaKey"
          class="flex items-center gap-2.5 px-3 py-2 text-sm"
        >
          <input
            type="checkbox"
            class="accent-primary"
            :checked="selected.has(item.MediaKey)"
            :aria-label="`Select ${item.FileName}`"
            @change="toggle(item.MediaKey)"
          >
          <component
            :is="item.IsVideo ? Film : Image"
            class="size-4 shrink-0 text-muted-foreground"
          />
          <div class="min-w-0 flex-1">
            <p class="truncate">
              {{ item.FileName || '(no name)' }}
            </p>
            <p class="truncate text-[11px] text-muted-foreground">
              <template v-if="item.TrashedAt">
                deleted {{ formatTimeAgo(item.TrashedAt, minuteClock) }}
              </template>
              <template v-if="daysLeft(item)">
                · {{ daysLeft(item) }}
              </template>
              <template v-if="item.RepairedPhoto">
                · from Live Photo repair
              </template>
              <template v-if="item.CopyInLibrary">
                · copy in library
              </template>
            </p>
          </div>
          <span class="shrink-0 text-xs text-muted-foreground tabular-nums">{{ formatBytes(item.SizeBytes) }}</span>
          <button
            type="button"
            class="flex size-6 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
            title="Open in Google Photos"
            @click="openInGooglePhotos(item.MediaKey)"
          >
            <ExternalLink class="size-3.5" />
          </button>
        </li>
        <li
          v-if="items.length > rows.length"
          class="px-3 py-2 text-xs text-muted-foreground"
        >
          + {{ (items.length - rows.length).toLocaleString() }} more (Select all includes them)
        </li>
      </ul>
    </template>
    <p
      v-else-if="binStore.report"
      class="px-1 text-sm text-muted-foreground"
    >
      The bin is empty.
    </p>
  </div>
</template>
