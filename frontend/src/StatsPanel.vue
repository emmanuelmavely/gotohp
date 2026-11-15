<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { toast } from 'vue-sonner'
import { CircleCheck, CircleX, ExternalLink, RefreshCw, ScanSearch, SkipForward, Wrench } from '@lucide/vue'
import { LibraryService } from '../bindings/app/internal/gui'
import type {
  LivePhotoRepairCandidate,
  LivePhotoRepairOutcome,
  LivePhotoRepairReport,
  UploadStats,
} from '../bindings/app/core'
import { Button } from '@/components/ui/button'
import LibraryScanLive from '@/components/LibraryScanLive.vue'
import LivePhotoRepairLive from '@/components/LivePhotoRepairLive.vue'
import BinPanel from '@/components/BinPanel.vue'
import { liveRepair, runLivePhotoRepair } from '@/utils/liveRepair'
import { openInGooglePhotos } from '@/utils/googlePhotos'
import {
  accountStorage,
  beginLibraryScan,
  calculateAccountStorage,
  endLibraryScan,
  libraryScan,
  minuteClock,
  storageCountedAt,
} from '@/utils/accountStorage'
import { debugLog } from '@/utils/debug'
import { formatBytes, formatDuration, formatTimeAgo } from '@/utils/format'

const DAYS_SHOWN = 30

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

// --- Account storage ---------------------------------------------------------
const storage = computed(() => accountStorage.data)

const syncedAtText = computed(() => {
  const countedAt = storageCountedAt(storage.value)
  if (!countedAt) return ''
  return `${formatTimeAgo(countedAt, minuteClock.value)} · ${new Date(countedAt).toLocaleString()}`
})

async function refreshStorage() {
  if (!await calculateAccountStorage()) {
    toast.error('Could not read account storage', { description: accountStorage.error })
  }
}

// --- Cumulative statistics ---------------------------------------------------
const stats = ref<UploadStats | null>(null)

async function loadStats() {
  try {
    stats.value = await LibraryService.UploadStats()
  } catch (error) {
    debugLog('error', 'frontend', 'UploadStats failed', errorText(error))
    toast.error('Could not load statistics', { description: errorText(error) })
  }
}

onMounted(loadStats)

const averageSpeed = computed(() => {
  const current = stats.value
  if (!current || current.activeSeconds <= 0) return '--'
  return `${formatBytes(current.uploadedBytes / current.activeSeconds)}/s`
})

const totals = computed<[string, string][]>(() => {
  const current = stats.value
  if (!current) return []
  return [
    ['Uploaded', formatBytes(current.uploadedBytes, 2)],
    ['Downloaded', formatBytes(current.downloadedBytes, 2)],
    ['Average speed', averageSpeed.value],
    ['Files uploaded', current.uploadedFiles.toLocaleString()],
    ['Already in library', current.skippedFiles.toLocaleString()],
    ['Failed', current.failedFiles.toLocaleString()],
    ['Upload runs', current.sessions.toLocaleString()],
    ['Time uploading', formatDuration(current.activeSeconds)],
  ]
})

function localDayKey(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

interface DayBar {
  key: string
  bytes: number
  /** Bar height in percent of the busiest day. */
  height: number
}

const dailyBars = computed<DayBar[]>(() => {
  const daily = stats.value?.daily ?? {}
  const days: DayBar[] = []
  const today = new Date()
  for (let offset = DAYS_SHOWN - 1; offset >= 0; offset--) {
    const day = new Date(today.getFullYear(), today.getMonth(), today.getDate() - offset)
    const key = localDayKey(day)
    days.push({ key, bytes: daily[key] ?? 0, height: 0 })
  }
  const busiest = Math.max(...days.map(day => day.bytes))
  for (const day of days) {
    // Any upload at all stays visible next to a much busier day.
    day.height = busiest > 0 && day.bytes > 0 ? Math.max(4, (day.bytes / busiest) * 100) : 0
  }
  return days
})

const busiestDay = computed(() =>
  dailyBars.value.reduce<DayBar | null>((best, day) => (day.bytes > (best?.bytes ?? 0) ? day : best), null),
)

// --- Live Photo repair -------------------------------------------------------
const report = ref<LivePhotoRepairReport | null>(null)
const isScanning = ref(false)
const isConfirming = ref(false)
const selectedStems = ref<string[]>([])
// The batch lives in a shared store, so it survives closing this sheet
const isRepairing = computed(() => liveRepair.running)
const outcomes = computed<LivePhotoRepairOutcome[]>(() => liveRepair.outcomes)

/** The repaired photo's media key; backends built before the link do not send it. */
function outcomeMediaKey(outcome: LivePhotoRepairOutcome): string {
  return (outcome as LivePhotoRepairOutcome & { PhotoMediaKey?: string }).PhotoMediaKey ?? ''
}

/** Live Photos repaired over all time; older backends do not report it yet. */
const repairedAllTime = computed(() =>
  (stats.value as (UploadStats & { repairedLivePhotos?: number }) | null)?.repairedLivePhotos ?? 0,
)

const candidates = computed<LivePhotoRepairCandidate[]>(() => report.value?.Candidates ?? [])
const ambiguous = computed<string[]>(() => report.value?.Ambiguous ?? [])

/** Names with a photo and a video that are not safe to join, with the reason. */
interface RepairExclusion {
  Stem: string
  Reason: string
}
const excluded = computed<RepairExclusion[]>(() =>
  (report.value as (LivePhotoRepairReport & { Excluded?: RepairExclusion[] | null }) | null)?.Excluded ?? [],
)
/** Pairs joined before; Google still lists their motion part as a separate video. */
interface AlreadyRepaired {
  Stem: string
  PhotoFileName: string
  PhotoMediaKey: string
  Reason: string
}
const alreadyRepaired = computed<AlreadyRepaired[]>(() =>
  (report.value as (LivePhotoRepairReport & { AlreadyRepaired?: AlreadyRepaired[] | null }) | null)?.AlreadyRepaired ?? [],
)
const allSelected = computed(() =>
  candidates.value.length > 0 && selectedStems.value.length === candidates.value.length,
)

// One library scan at a time: both scans walk the same library and share the live view
const isLibraryBusy = computed(() => libraryScan.startedAt > 0 || libraryScan.repairing)

async function scanForRepairs() {
  if (isLibraryBusy.value) return
  isScanning.value = true
  isConfirming.value = false
  beginLibraryScan()
  try {
    report.value = await LibraryService.LivePhotoRepairs()
    selectedStems.value = []
    liveRepair.outcomes = []
  } catch (error) {
    debugLog('error', 'frontend', 'LivePhotoRepairs failed', errorText(error))
    toast.error('Could not scan the library', { description: errorText(error) })
  } finally {
    endLibraryScan()
    isScanning.value = false
  }
}

function toggleAll() {
  selectedStems.value = allSelected.value ? [] : candidates.value.map(candidate => candidate.Stem)
  isConfirming.value = false
}

function toggleStem(stem: string) {
  selectedStems.value = selectedStems.value.includes(stem)
    ? selectedStems.value.filter(selected => selected !== stem)
    : [...selectedStems.value, stem]
  isConfirming.value = false
}

async function repairSelected() {
  // Never send an empty list; the backend refuses it as well.
  const stems = [...selectedStems.value]
  if (stems.length === 0) return

  isConfirming.value = false
  try {
    const result = await runLivePhotoRepair(stems)
    const repaired = result.filter(outcome => outcome.Status === 'repaired').length
    if (result.length > 0 && repaired === result.length) {
      toast.success(`Repaired ${repaired} Live Photo(s).`)
    } else if (result.length > 0) {
      toast.warning(`Repaired ${repaired} of ${result.length}.`, { description: 'See the list for what was skipped or failed.' })
    }
    // Repaired pairs are gone from the library, so drop them from the list.
    const done = new Set(result.filter(outcome => outcome.Status === 'repaired').map(outcome => outcome.Stem))
    if (report.value) {
      report.value.Candidates = report.value.Candidates.filter(candidate => !done.has(candidate.Stem))
    }
    selectedStems.value = selectedStems.value.filter(stem => !done.has(stem))
  } catch (error) {
    debugLog('error', 'frontend', 'RepairLivePhotos failed', errorText(error))
    toast.error('Live Photo repair failed', { description: errorText(error) })
  } finally {
    // Re-uploaded videos and the repair count are now in the totals.
    await loadStats()
  }
}
</script>

<template>
  <div class="mx-auto flex w-full max-w-2xl flex-col gap-4 px-4 pb-5">
    <!-- Account storage -->
    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Account storage
      </h3>
      <LibraryScanLive v-if="accountStorage.loading" />
      <div
        v-else
        class="rounded-xl border bg-card p-3"
      >
        <div class="flex items-center gap-3">
          <div class="min-w-0 flex-1">
            <template v-if="storage">
              <p class="text-2xl leading-none font-semibold tracking-tight tabular-nums">
                {{ formatBytes(storage.Summary.TotalBytes, 2) }}
              </p>
              <p class="mt-1.5 text-xs text-muted-foreground tabular-nums">
                used by {{ storage.Summary.ItemCount.toLocaleString() }} files that count against your quota
                <template v-if="storageCountedAt(storage)">
                  · counted {{ formatTimeAgo(storageCountedAt(storage), minuteClock) }}
                </template>
              </p>
            </template>
            <p
              v-else
              class="text-sm text-muted-foreground"
            >
              Counts the storage your photos really use, from your library. The first count can take several minutes.
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            class="shrink-0 cursor-pointer"
            :disabled="isLibraryBusy"
            @click="refreshStorage"
          >
            <RefreshCw />
            {{ storage ? 'Refresh' : 'Calculate' }}
          </Button>
        </div>
        <dl
          v-if="storage"
          class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 border-t pt-3 text-xs"
        >
          <dt class="text-muted-foreground">
            Library entries
          </dt>
          <dd class="text-right tabular-nums">
            {{ storage.Items.toLocaleString() }}
          </dd>
          <dt class="text-muted-foreground">
            Size of everything in the library
          </dt>
          <dd class="text-right tabular-nums">
            {{ formatBytes(storage.LibraryBytes, 2) }}
          </dd>
          <template v-if="storage.Summary.TrashedBytes > 0">
            <dt class="text-amber-300">
              In trash, still counted
            </dt>
            <dd class="text-right text-amber-300 tabular-nums">
              {{ formatBytes(storage.Summary.TrashedBytes, 2) }} · {{ storage.Summary.TrashedCount.toLocaleString() }} files
            </dd>
          </template>
          <dt class="text-muted-foreground">
            Counted
          </dt>
          <dd class="text-right tabular-nums">
            {{ syncedAtText }}
          </dd>
        </dl>
        <p
          v-if="accountStorage.error"
          class="mt-3 text-xs break-words text-destructive"
        >
          {{ accountStorage.error }}
        </p>
      </div>
    </section>

    <!-- Totals -->
    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        This app, all time
      </h3>
      <div class="grid grid-cols-2 gap-1.5 sm:grid-cols-4">
        <div
          v-for="[label, value] in totals"
          :key="label"
          class="rounded-lg border bg-card px-3 py-2"
        >
          <p class="text-[10px] tracking-wide text-muted-foreground uppercase">
            {{ label }}
          </p>
          <p class="mt-0.5 truncate text-sm font-medium tabular-nums">
            {{ value }}
          </p>
        </div>
      </div>
    </section>

    <!-- Daily uploads -->
    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Uploaded per day · last {{ DAYS_SHOWN }} days
      </h3>
      <div class="rounded-xl border bg-card p-3">
        <div
          v-if="busiestDay"
          class="flex h-24 items-end gap-px"
          role="img"
          :aria-label="`Bytes uploaded per day. Busiest day ${busiestDay.key}: ${formatBytes(busiestDay.bytes, 2)}.`"
        >
          <div
            v-for="day in dailyBars"
            :key="day.key"
            class="flex h-full min-w-0 flex-1 items-end"
            :title="`${day.key}: ${formatBytes(day.bytes, 2)}`"
          >
            <div
              class="w-full rounded-t-sm bg-primary/80 hover:bg-primary"
              :style="{ height: `${day.height}%` }"
            />
          </div>
        </div>
        <p
          v-else
          class="py-6 text-center text-xs text-muted-foreground"
        >
          Nothing uploaded in this period yet.
        </p>
        <div class="mt-1.5 flex justify-between border-t pt-1.5 text-[10px] text-muted-foreground tabular-nums">
          <span>{{ dailyBars[0]?.key }}</span>
          <span v-if="busiestDay">busiest: {{ busiestDay.key }} · {{ formatBytes(busiestDay.bytes, 2) }}</span>
          <span>today</span>
        </div>
      </div>
    </section>

    <!-- Live Photo repair -->
    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Live Photo repair
      </h3>
      <div class="rounded-xl border bg-card p-3">
        <div class="flex items-start gap-3">
          <p class="min-w-0 flex-1 text-xs leading-snug text-muted-foreground">
            Finds photos whose Live Photo video was uploaded as a separate item and joins them: same file name, an HEIC original, taken at the same moment.
            <span
              v-if="repairedAllTime > 0"
              class="text-foreground"
            >{{ repairedAllTime.toLocaleString() }} repaired so far.</span>
          </p>
          <Button
            variant="outline"
            size="sm"
            class="shrink-0 cursor-pointer"
            :disabled="isLibraryBusy || isRepairing"
            @click="scanForRepairs"
          >
            <ScanSearch :class="isScanning && 'animate-pulse'" />
            {{ isScanning ? 'Scanning…' : report ? 'Scan again' : 'Scan library' }}
          </Button>
        </div>

        <LibraryScanLive
          v-if="isScanning"
          class="mt-3"
        />

        <!-- One pair at a time; continues in the background if this sheet is closed -->
        <LivePhotoRepairLive
          v-else-if="isRepairing"
          class="mt-3"
        />

        <template v-else-if="report">
          <p
            v-if="candidates.length === 0"
            class="mt-3 border-t pt-3 text-xs text-muted-foreground"
          >
            No separated Live Photo pairs found.
          </p>
          <template v-else>
            <label class="mt-3 flex cursor-pointer items-center gap-2 border-t pt-3 text-xs font-medium select-none">
              <input
                type="checkbox"
                class="accent-primary"
                :checked="allSelected"
                :disabled="isRepairing"
                @change="toggleAll"
              >
              {{ candidates.length }} pair(s) found · {{ selectedStems.length }} selected
            </label>
            <ul class="mt-2 max-h-56 divide-y overflow-y-auto rounded-lg border">
              <li
                v-for="candidate in candidates"
                :key="candidate.Stem"
              >
                <label class="flex cursor-pointer items-start gap-2 px-2.5 py-1.5 hover:bg-accent/40">
                  <input
                    type="checkbox"
                    class="mt-0.5 accent-primary"
                    :checked="selectedStems.includes(candidate.Stem)"
                    :disabled="isRepairing"
                    @change="toggleStem(candidate.Stem)"
                  >
                  <span class="min-w-0 flex-1 text-xs leading-snug">
                    <span class="flex items-center gap-1.5">
                      <span class="min-w-0 truncate">
                        {{ candidate.Photo?.FileName }}
                        <span class="text-muted-foreground tabular-nums">· {{ formatBytes(candidate.Photo?.SizeBytes ?? 0) }}</span>
                      </span>
                      <button
                        v-if="candidate.Photo?.MediaKey"
                        type="button"
                        class="ml-auto flex shrink-0 cursor-pointer items-center rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                        title="Open the photo in Google Photos"
                        :aria-label="`Open ${candidate.Photo.FileName} in Google Photos`"
                        @click.stop.prevent="openInGooglePhotos(candidate.Photo.MediaKey)"
                      >
                        <ExternalLink class="size-3" />
                      </button>
                    </span>
                    <span class="block truncate text-muted-foreground">
                      + {{ candidate.Video?.FileName }}
                      <span class="tabular-nums">· {{ formatBytes(candidate.Video?.SizeBytes ?? 0) }}</span>
                    </span>
                  </span>
                </label>
              </li>
            </ul>

            <!-- Confirmation -->
            <div
              v-if="isConfirming"
              class="mt-3 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3"
            >
              <p class="text-xs leading-snug text-amber-100/90">
                For each of the {{ selectedStems.length }} selected pair(s) this downloads the video, uploads it again
                attached to its photo, and moves the separate video to the trash in Google Photos. Nothing on this
                computer is changed. Pairs that cannot be verified are skipped.
              </p>
              <div class="mt-2.5 grid grid-cols-2 gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  class="cursor-pointer"
                  @click="isConfirming = false"
                >
                  Cancel
                </Button>
                <Button
                  size="sm"
                  class="cursor-pointer"
                  @click="repairSelected"
                >
                  Repair {{ selectedStems.length }} pair(s)
                </Button>
              </div>
            </div>
            <Button
              v-else
              size="sm"
              class="mt-3 w-full cursor-pointer"
              :disabled="selectedStems.length === 0 || isRepairing"
              @click="isConfirming = true"
            >
              <Wrench :class="isRepairing && 'animate-pulse'" />
              {{ isRepairing ? 'Repairing… this can take a while' : `Repair selected (${selectedStems.length})` }}
            </Button>
          </template>

          <p
            v-if="ambiguous.length"
            class="mt-3 text-[11px] leading-snug break-words text-muted-foreground"
          >
            {{ ambiguous.length }} name(s) match more than one photo or video and are left alone:
            {{ ambiguous.slice(0, 12).join(', ') }}{{ ambiguous.length > 12 ? '…' : '' }}
          </p>

          <details
            v-if="alreadyRepaired.length"
            class="mt-2 text-[11px] leading-snug text-muted-foreground"
          >
            <summary class="cursor-pointer select-none hover:text-foreground">
              {{ alreadyRepaired.length }} already a Live Photo, so not offered again
            </summary>
            <ul class="mt-1.5 max-h-40 divide-y overflow-y-auto rounded-lg border">
              <li
                v-for="done in alreadyRepaired"
                :key="done.Stem"
                class="flex items-start gap-1.5 px-2.5 py-1.5"
              >
                <span class="min-w-0 flex-1">
                  <span class="text-foreground">{{ done.PhotoFileName || done.Stem }}</span> · {{ done.Reason }}
                </span>
                <button
                  v-if="done.PhotoMediaKey"
                  type="button"
                  class="flex shrink-0 cursor-pointer items-center rounded p-0.5 hover:bg-accent hover:text-foreground"
                  title="Open the photo in Google Photos"
                  :aria-label="`Open ${done.PhotoFileName || done.Stem} in Google Photos`"
                  @click="openInGooglePhotos(done.PhotoMediaKey)"
                >
                  <ExternalLink class="size-3" />
                </button>
              </li>
            </ul>
          </details>

          <details
            v-if="excluded.length"
            class="mt-2 text-[11px] leading-snug text-muted-foreground"
          >
            <summary class="cursor-pointer select-none hover:text-foreground">
              {{ excluded.length }} name(s) skipped to avoid a wrong match (JPEG only, or not taken at the same moment)
            </summary>
            <ul class="mt-1.5 max-h-40 divide-y overflow-y-auto rounded-lg border">
              <li
                v-for="exclusion in excluded"
                :key="exclusion.Stem"
                class="px-2.5 py-1.5"
              >
                <span class="text-foreground">{{ exclusion.Stem }}</span> · {{ exclusion.Reason }}
              </li>
            </ul>
          </details>
        </template>

        <!-- Results of the last repair -->
        <ul
          v-if="outcomes.length"
          class="mt-3 max-h-64 divide-y overflow-y-auto rounded-lg border"
        >
          <li
            v-for="outcome in outcomes"
            :key="outcome.Stem"
            class="flex gap-2 px-2.5 py-1.5"
          >
            <CircleCheck
              v-if="outcome.Status === 'repaired'"
              class="mt-0.5 size-3.5 shrink-0 text-primary"
            />
            <SkipForward
              v-else-if="outcome.Status === 'skipped'"
              class="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
            />
            <CircleX
              v-else
              class="mt-0.5 size-3.5 shrink-0 text-destructive"
            />
            <div class="min-w-0 flex-1 text-xs leading-snug">
              <p class="flex items-center gap-1.5">
                <span class="min-w-0 truncate">{{ outcome.Stem }} <span class="text-muted-foreground">· {{ outcome.Status }}</span></span>
                <button
                  v-if="outcomeMediaKey(outcome)"
                  type="button"
                  class="ml-auto flex shrink-0 cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-accent hover:text-foreground"
                  title="Open the photo in Google Photos"
                  @click="openInGooglePhotos(outcomeMediaKey(outcome))"
                >
                  <ExternalLink class="size-3" />
                  Open
                </button>
              </p>
              <p
                v-if="outcome.Message"
                class="break-words text-muted-foreground"
              >
                {{ outcome.Message }}
              </p>
            </div>
          </li>
        </ul>
      </div>
    </section>

    <!-- Bin -->
    <BinPanel />
  </div>
</template>
