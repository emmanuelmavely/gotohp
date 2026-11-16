<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Events } from '@wailsio/runtime'
import { AlertTriangle, CircleCheck, CircleX, ExternalLink, FolderPlus, SkipForward, X } from '@lucide/vue'
import * as guiBindings from '../../bindings/app/internal/gui'
import { Button } from '@/components/ui/button'
import LibraryScanLive from '@/components/LibraryScanLive.vue'
import { beginLibraryScan, endLibraryScan, libraryScan } from '@/utils/accountStorage'
import { debugLog } from '@/utils/debug'
import { formatBytes } from '@/utils/format'
import { openInGooglePhotos } from '@/utils/googlePhotos'
import { uploadManager } from '@/utils/UploadManager'

// --- Backend shapes (reached by name so older bindings still build) -----------
interface ConversionItem {
  MediaKey: string
  FileName: string
  SHA1Hex: string
  SizeBytes: number
  QuotaBytes: number
  LocalPath: string
  Folder: string
  CapturedAt: number
  Favorite: boolean
  Archived: boolean
  Edited: boolean
}
interface ConversionReport {
  CountedFiles: number
  CountedBytes: number
  Convertible: ConversionItem[] | null
  ConvertibleBytes: number
  NotOnComputer: number
  NotOnComputerBytes: number
  SameNameDifferent?: number
  SameNameDifferentBytes?: number
  SameNameExamples?: { FileName: string, LocalPath: string, RemoteSize: number, LocalSize: number }[] | null
  SizeMatched?: number
  Excluded: { FileName: string, MediaKey: string, Bytes: number, Reason: string }[] | null
  Folders: { Folder: string, Files: number, Bytes: number }[] | null
  ScannedFiles: number
  Downloadable?: ConversionItem[] | null
  DownloadableBytes?: number
  NotDownloadable?: { Reason: string, Files: number, Bytes: number }[] | null
}
interface DownloadOutcome {
  MediaKey: string
  FileName: string
  LocalPath: string
  Status: 'downloaded' | 'skipped' | 'failed'
  Message: string
}
interface DownloadProgress {
  Index: number
  Total: number
  FileName: string
  BytesDone: number
  BytesTotal: number
  DoneBytes: number
  Outcome?: DownloadOutcome | null
}
interface DeleteOutcome {
  MediaKey: string
  FileName: string
  Status: 'deleted' | 'skipped' | 'failed'
  Message: string
}
interface ConvertProgress {
  Phase: 'downloading' | 'deleting' | 'uploading'
  Done: number
  Total: number
  Batch: number
  Batches: number
  FileName: string
  BytesDone: number
  BytesTotal: number
  Outcome?: DeleteOutcome | null
}
interface ScanProgress {
  Seen: number
  Hashed: number
  Matched: number
  Folder: string
}
interface ConversionStatus {
  FileName: string
  LocalPath: string
  SHA1Hex: string
  QuotaBytes: number
  State: 'converted' | 'ready' | 'in-bin' | 'counted'
  LocalMissing: boolean
}
interface QuotaApi {
  QuotaConversionScan: (folders: string[]) => Promise<ConversionReport>
  QuotaConvert: (mediaKeys: string[], acknowledged: boolean) => Promise<DeleteOutcome[]>
  QuotaConversionStatus: () => Promise<ConversionStatus[]>
  QuotaUploadReady: () => Promise<number>
  QuotaForgetConverted: () => Promise<void>
  QuotaRecordedCount: () => Promise<number>
  QuotaDownload: (mediaKeys: string[], folder: string) => Promise<{ Outcomes: DownloadOutcome[], Report: ConversionReport }>
}
const bindings = guiBindings as unknown as {
  LibraryService?: Partial<QuotaApi>
  DialogService?: { PickPhotosFolder?: () => Promise<string>, PickDownloadFolder?: () => Promise<string> }
}
const api = bindings.LibraryService as QuotaApi | undefined
const available = Boolean(api?.QuotaConversionScan && bindings.DialogService?.PickPhotosFolder)

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

// --- Folders to search, remembered on this computer ----------------------------
const FOLDERS_KEY = 'gotohp.quotaFolders'
function readFolders(): string[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(FOLDERS_KEY) ?? '[]')
    return Array.isArray(parsed) ? parsed.filter((value): value is string => typeof value === 'string') : []
  } catch {
    return []
  }
}
const folders = ref<string[]>(readFolders())
watch(folders, (value) => {
  try {
    localStorage.setItem(FOLDERS_KEY, JSON.stringify(value))
  } catch {
    // Without storage the list lasts for this session.
  }
}, { deep: true })

async function addFolder() {
  try {
    const folder = await bindings.DialogService?.PickPhotosFolder?.()
    if (folder && !folders.value.includes(folder)) folders.value.push(folder)
  } catch (error) {
    debugLog('error', 'frontend', 'PickPhotosFolder failed', errorText(error))
  }
}

function removeFolder(folder: string) {
  folders.value = folders.value.filter(value => value !== folder)
}

// --- Scan ----------------------------------------------------------------------
const report = ref<ConversionReport | null>(null)
/** The folders the shown report searched. */
const reportFolders = ref<string[]>([])
const scanning = ref(false)
const scanProgress = ref<ScanProgress | null>(null)
const selected = ref(new Set<string>())

const items = computed<ConversionItem[]>(() => report.value?.Convertible ?? [])
const excluded = computed(() => report.value?.Excluded ?? [])
const folderTotals = computed(() => report.value?.Folders ?? [])
const selectedItems = computed(() => items.value.filter(item => selected.value.has(item.MediaKey)))
const selectedBytes = computed(() => selectedItems.value.reduce((sum, item) => sum + item.QuotaBytes, 0))
const selectedLosses = computed(() => ({
  favorite: selectedItems.value.filter(item => item.Favorite).length,
  edited: selectedItems.value.filter(item => item.Edited).length,
  archived: selectedItems.value.filter(item => item.Archived).length,
}))

const uploadBusy = computed(() =>
  uploadManager.state.isUploading || uploadManager.state.isStarting || Boolean(uploadManager.state.paused),
)
const libraryBusy = computed(() => libraryScan.startedAt > 0 || libraryScan.repairing)

async function scan() {
  if (!api || folders.value.length === 0 || scanning.value || libraryBusy.value) return
  scanning.value = true
  scanProgress.value = null
  report.value = null
  selected.value = new Set()
  acknowledged.value = false
  beginLibraryScan()
  try {
    reportFolders.value = [...folders.value]
    report.value = await api.QuotaConversionScan([...folders.value])
  } catch (error) {
    toast.error('Could not search for local copies', { description: errorText(error) })
  } finally {
    endLibraryScan()
    scanning.value = false
  }
}

function toggle(key: string) {
  const next = new Set(selected.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  selected.value = next
  acknowledged.value = false
}

function setAll(on: boolean) {
  selected.value = new Set(on ? items.value.map(item => item.MediaKey) : [])
  acknowledged.value = false
}

function folderSelected(folder: string): boolean {
  const inFolder = items.value.filter(item => item.Folder === folder)
  return inFolder.length > 0 && inFolder.every(item => selected.value.has(item.MediaKey))
}

function toggleFolder(folder: string) {
  const on = !folderSelected(folder)
  const next = new Set(selected.value)
  for (const item of items.value) {
    if (item.Folder !== folder) continue
    if (on) next.add(item.MediaKey)
    else next.delete(item.MediaKey)
  }
  selected.value = next
  acknowledged.value = false
}

function shortFolder(folder: string): string {
  const parts = folder.split(/[\\/]/).filter(Boolean)
  return parts.length <= 3 ? folder : `…\\${parts.slice(-2).join('\\')}`
}

// --- Download originals that are not on this computer -------------------------------
const DOWNLOAD_FOLDER_KEY = 'gotohp.quotaDownloadFolder'
function readDownloadFolder(): string {
  try {
    return localStorage.getItem(DOWNLOAD_FOLDER_KEY) ?? ''
  } catch {
    return ''
  }
}
const downloadFolder = ref(readDownloadFolder())
watch(downloadFolder, (value) => {
  try {
    localStorage.setItem(DOWNLOAD_FOLDER_KEY, value)
  } catch {
    // Without storage the choice lasts for this session.
  }
})
const downloadable = computed(() => report.value?.Downloadable ?? [])
const downloadableSize = computed(() => downloadable.value.reduce((sum, item) => sum + item.SizeBytes, 0))
const notDownloadable = computed(() => report.value?.NotDownloadable ?? [])
const downloading = ref(false)
const downloadProgress = ref<DownloadProgress | null>(null)
const downloadOutcomes = ref<DownloadOutcome[]>([])
const downloadProblems = computed(() => downloadOutcomes.value.filter(outcome => outcome.Status !== 'downloaded'))
const downloadTotalBytes = ref(0)
const canDownload = Boolean(api?.QuotaDownload && bindings.DialogService?.PickDownloadFolder)

async function chooseDownloadFolder() {
  try {
    const folder = await bindings.DialogService?.PickDownloadFolder?.()
    if (folder) downloadFolder.value = folder
  } catch (error) {
    debugLog('error', 'frontend', 'PickDownloadFolder failed', errorText(error))
  }
}

async function downloadOriginals() {
  if (!api?.QuotaDownload || !downloadFolder.value || downloading.value || downloadable.value.length === 0) return
  downloading.value = true
  downloadProgress.value = null
  downloadOutcomes.value = []
  downloadTotalBytes.value = downloadableSize.value
  try {
    const result = await api.QuotaDownload([], downloadFolder.value)
    report.value = result.Report
    const saved = result.Outcomes.filter(outcome => outcome.Status === 'downloaded').length
    toast.success(`Downloaded ${saved} of ${result.Outcomes.length} original(s)`, {
      description: saved > 0 ? 'They are now listed as ready to convert.' : undefined,
    })
    // Later searches include the download folder.
    if (!folders.value.includes(downloadFolder.value)) folders.value.push(downloadFolder.value)
  } catch (error) {
    toast.error('Download stopped', { description: errorText(error) })
  } finally {
    downloading.value = false
  }
}

// --- Convert -------------------------------------------------------------------
const acknowledged = ref(false)
const converting = ref(false)
const progress = ref<ConvertProgress | null>(null)
const outcomes = ref<DeleteOutcome[]>([])
const problemOutcomes = computed(() => outcomes.value.filter(outcome => outcome.Status !== 'deleted'))

async function convert() {
  if (!api || selectedItems.value.length === 0 || !acknowledged.value || uploadBusy.value) return
  converting.value = true
  progress.value = null
  outcomes.value = []
  try {
    const result = await api.QuotaConvert(selectedItems.value.map(item => item.MediaKey), true)
    const deleted = result.filter(outcome => outcome.Status === 'deleted').length
    toast.success(`Converted ${deleted} of ${result.length} file(s)`, {
      description: 'Check status below once the uploads finish to confirm they no longer count.',
    })
    // Converted files are gone from the counted list.
    const done = new Set(result.filter(outcome => outcome.Status === 'deleted').map(outcome => outcome.MediaKey))
    if (report.value) {
      report.value = { ...report.value, Convertible: items.value.filter(item => !done.has(item.MediaKey)) }
    }
    selected.value = new Set()
    acknowledged.value = false
    await loadRecordedCount()
  } catch (error) {
    toast.error('Conversion stopped', { description: errorText(error) })
  } finally {
    converting.value = false
  }
}

function stop() {
  Events.Emit('quotaConvertCancel')
}

// --- Earlier conversions ----------------------------------------------------------
const recordedCount = ref(0)
const statuses = ref<ConversionStatus[] | null>(null)
const checking = ref(false)
const stateCounts = computed(() => {
  const counts = { converted: 0, ready: 0, 'in-bin': 0, counted: 0, missing: 0 }
  for (const status of statuses.value ?? []) {
    counts[status.State] += 1
    if (status.State === 'ready' && status.LocalMissing) counts.missing += 1
  }
  return counts
})
const unfinished = computed(() => (statuses.value ?? []).filter(status => status.State !== 'converted'))

async function loadRecordedCount() {
  try {
    recordedCount.value = (await api?.QuotaRecordedCount?.()) ?? 0
  } catch {
    recordedCount.value = 0
  }
}

async function checkStatus() {
  if (!api || checking.value || libraryBusy.value) return
  checking.value = true
  beginLibraryScan()
  try {
    statuses.value = await api.QuotaConversionStatus()
  } catch (error) {
    toast.error('Could not check the conversions', { description: errorText(error) })
  } finally {
    endLibraryScan()
    checking.value = false
  }
}

async function uploadReady() {
  if (!api) return
  try {
    const started = await api.QuotaUploadReady()
    toast.info(started > 0 ? `Uploading ${started} file(s) without quota` : 'Nothing is waiting to upload')
  } catch (error) {
    toast.error('Could not start the upload', { description: errorText(error) })
  }
}

async function forgetConverted() {
  if (!api) return
  try {
    await api.QuotaForgetConverted()
    statuses.value = (statuses.value ?? []).filter(status => status.State !== 'converted')
    await loadRecordedCount()
  } catch (error) {
    toast.error('Could not clear the list', { description: errorText(error) })
  }
}

const stateText: Record<ConversionStatus['State'], string> = {
  converted: 'converted',
  ready: 'deleted, not uploaded again yet',
  'in-bin': 'still in the bin',
  counted: 'counts again (restored or uploaded with quota)',
}

// --- Convert everything ------------------------------------------------------------
interface ConvertAllPlan {
  CountedFiles: number
  CountedBytes: number
  Files: number
  Bytes: number
  DownloadBytes: number
  SaverFiles: number
  LivePhotos: number
  LivePhotoBytes: number
  Waiting: number
  DefaultFolder: string
}
interface ConvertAllResult {
  Total: number
  Converted: number
  Problems: DeleteOutcome[]
}
const allApi = bindings.LibraryService as {
  QuotaConvertAllPlan?: () => Promise<ConvertAllPlan>
  QuotaConvertAll?: (folder: string, limit: number, acknowledged: boolean) => Promise<ConvertAllResult>
} | undefined

const allPlan = ref<ConvertAllPlan | null>(null)
const planning = ref(false)
const convertingAll = ref(false)
const acknowledgedAll = ref(false)
const allProgress = ref<ConvertProgress | null>(null)
const allResult = ref<ConvertAllResult | null>(null)
const backupFolder = computed(() => downloadFolder.value || allPlan.value?.DefaultFolder || '')

const allPhaseText = computed(() => {
  const progress = allProgress.value
  if (!progress) return 'Starting…'
  const batch = progress.Batches ? ` · batch ${progress.Batch} of ${progress.Batches}` : ''
  if (progress.Phase === 'downloading') {
    const bytes = progress.BytesTotal > 0 ? ` (${formatBytes(progress.BytesDone)} of ${formatBytes(progress.BytesTotal)})` : ''
    return `Downloading ${progress.FileName}${bytes}${batch}`
  }
  if (progress.Phase === 'deleting') return `Deleting the counted copy of ${progress.FileName}${batch}`
  return `Uploading again without counting${batch} (see the main screen)`
})

async function loadPlan() {
  if (!allApi?.QuotaConvertAllPlan || planning.value || libraryBusy.value) return
  planning.value = true
  beginLibraryScan()
  try {
    allPlan.value = await allApi.QuotaConvertAllPlan()
  } catch (error) {
    toast.error('Could not read your storage', { description: errorText(error) })
  } finally {
    endLibraryScan()
    planning.value = false
  }
}

async function chooseBackupFolder() {
  await chooseDownloadFolder()
}

async function convertAll(limit = 0) {
  if (!allApi?.QuotaConvertAll || !allPlan.value || !acknowledgedAll.value || uploadBusy.value) return
  convertingAll.value = true
  allProgress.value = null
  allResult.value = null
  try {
    allResult.value = await allApi.QuotaConvertAll(backupFolder.value, limit, true)
    acknowledgedAll.value = false
    await loadRecordedCount()
  } catch (error) {
    toast.error('Conversion stopped', { description: errorText(error) })
  } finally {
    convertingAll.value = false
  }
  // Show what is left.
  await loadPlan()
}

// --- Events ----------------------------------------------------------------------
let offScan: (() => void) | undefined
let offConvert: (() => void) | undefined
let offDownload: (() => void) | undefined
onMounted(() => {
  if (!available) return
  void loadRecordedCount()
  offScan = Events.On('quotaScanProgress', (event: { data: ScanProgress }) => {
    scanProgress.value = event.data
  })
  offDownload = Events.On('quotaDownloadProgress', (event: { data: DownloadProgress }) => {
    downloadProgress.value = event.data
    if (event.data.Outcome) downloadOutcomes.value = [...downloadOutcomes.value, event.data.Outcome]
  })
  offConvert = Events.On('quotaConvertProgress', (event: { data: ConvertProgress }) => {
    if (convertingAll.value) {
      allProgress.value = event.data
      return
    }
    progress.value = event.data
    if (event.data.Outcome) outcomes.value = [...outcomes.value, event.data.Outcome]
  })
})
onUnmounted(() => {
  offScan?.()
  offConvert?.()
  offDownload?.()
})
</script>

<template>
  <section
    v-if="available"
    class="flex flex-col gap-1.5"
  >
    <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
      Make counted storage unlimited
    </h3>
    <div class="rounded-xl border bg-card p-3">
      <!-- Simple: convert everything -->
      <template v-if="!allPlan && !planning">
        <p class="text-xs leading-snug text-muted-foreground">
          Uploads the files that count against your storage again in a way that does not count, so the space is freed.
          First check what that would be.
        </p>
        <Button
          size="sm"
          class="mt-2.5 w-full cursor-pointer"
          :disabled="libraryBusy || converting || downloading"
          @click="loadPlan"
        >
          Check my counted storage
        </Button>
      </template>
      <LibraryScanLive v-else-if="planning" />
      <template v-else-if="allPlan">
        <p class="text-2xl leading-none font-semibold tracking-tight tabular-nums">
          {{ formatBytes(allPlan.CountedBytes, 2) }}
        </p>
        <p class="mt-1.5 text-xs text-muted-foreground tabular-nums">
          counted by {{ allPlan.CountedFiles.toLocaleString() }} files.
          <template v-if="allPlan.Files">
            Converting frees about <span class="text-foreground">{{ formatBytes(allPlan.Bytes, 2) }}</span>
            ({{ allPlan.Files.toLocaleString() }} files).
          </template>
          <template v-if="allPlan.LivePhotos">
            {{ allPlan.LivePhotos.toLocaleString() }} Live Photos ({{ formatBytes(allPlan.LivePhotoBytes) }}) stay as they are.
          </template>
          <template v-if="allPlan.Waiting">
            {{ allPlan.Waiting.toLocaleString() }} files from an earlier run are uploaded first.
          </template>
        </p>

        <div
          v-if="!convertingAll && allPlan.Files"
          class="mt-3 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-xs leading-snug text-amber-100/90"
        >
          <p class="flex items-center gap-1.5 font-semibold text-amber-200">
            <AlertTriangle class="size-3.5 shrink-0" />
            What happens to each file
          </p>
          <ul class="mt-1.5 flex list-disc flex-col gap-1 pl-4">
            <li>
              It is downloaded and kept in the backup folder below, then <strong>deleted from Google Photos for good</strong>
              and uploaded again. Albums, favourites, descriptions, edits made in Google Photos and sharing on it are lost.
            </li>
            <li v-if="allPlan.SaverFiles">
              {{ allPlan.SaverFiles.toLocaleString() }} files are stored in storage saver quality; Google has no original of
              them, so the stored copy is what gets uploaded again (same quality as now).
            </li>
            <li>
              It relies on how this unofficial app presents itself to Google. Google can change that or act on accounts
              that use it. Your own risk.
            </li>
          </ul>
        </div>

        <template v-if="!convertingAll && allPlan.Files">
          <div class="mt-3 flex items-center gap-2 text-xs">
            <span class="shrink-0 text-muted-foreground">Backup folder</span>
            <span
              class="min-w-0 flex-1 truncate font-mono"
              :title="backupFolder"
            >{{ backupFolder }}</span>
            <button
              type="button"
              class="shrink-0 cursor-pointer rounded px-1.5 py-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
              @click="chooseBackupFolder"
            >
              Change
            </button>
          </div>
          <p class="mt-0.5 text-[11px] text-muted-foreground">
            Needs about {{ formatBytes(allPlan.DownloadBytes, 1) }} of free space. The files stay there afterwards.
          </p>
          <label class="mt-3 flex cursor-pointer items-start gap-2 text-xs leading-snug select-none">
            <input
              v-model="acknowledgedAll"
              type="checkbox"
              class="mt-0.5 accent-destructive"
            >
            <span>I understand that each file is deleted from Google Photos and uploaded again, and that its albums,
              favourites, edits and sharing are lost.</span>
          </label>
          <div class="mt-2.5 grid grid-cols-[auto_1fr] gap-2">
            <Button
              variant="outline"
              size="sm"
              class="cursor-pointer"
              title="Convert the 10 oldest files first, then check that they stopped counting"
              :disabled="!acknowledgedAll || uploadBusy || libraryBusy || converting || downloading"
              @click="convertAll(10)"
            >
              Try 10 first
            </Button>
            <Button
              variant="destructive"
              size="sm"
              class="cursor-pointer"
              :disabled="!acknowledgedAll || uploadBusy || libraryBusy || converting || downloading"
              @click="convertAll()"
            >
              {{ uploadBusy ? 'Wait for the current upload to finish' : `Convert all ${allPlan.Files.toLocaleString()} files · free ${formatBytes(allPlan.Bytes, 2)}` }}
            </Button>
          </div>
          <p class="mt-1.5 text-[11px] leading-snug text-muted-foreground">
            Runs 20 files at a time and can be stopped. Running it again later continues where it stopped.
          </p>
        </template>
        <p
          v-else-if="!convertingAll"
          class="mt-3 text-xs text-muted-foreground"
        >
          Nothing left to convert.
        </p>

        <!-- Running -->
        <div
          v-if="convertingAll"
          class="mt-3 rounded-lg border border-primary/30 bg-primary/5 p-3 text-xs"
        >
          <div class="flex items-center gap-2">
            <span class="min-w-0 flex-1 truncate font-medium">{{ allPhaseText }}</span>
            <span
              v-if="allProgress && allProgress.Total"
              class="shrink-0 text-muted-foreground tabular-nums"
            >{{ allProgress.Done.toLocaleString() }} / {{ allProgress.Total.toLocaleString() }}</span>
          </div>
          <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-primary/15">
            <div
              class="h-full rounded-full bg-primary transition-[width] duration-500"
              :style="{ width: `${allProgress && allProgress.Total ? (allProgress.Done / allProgress.Total) * 100 : 0}%` }"
            />
          </div>
          <Button
            variant="outline"
            size="sm"
            class="mt-2.5 w-full cursor-pointer"
            @click="stop"
          >
            Stop after this batch
          </Button>
        </div>

        <div
          v-if="allResult && !convertingAll"
          class="mt-3 text-xs"
        >
          <p class="flex items-center gap-1.5 text-primary">
            <CircleCheck class="size-3.5" />
            {{ allResult.Converted.toLocaleString() }} of {{ allResult.Total.toLocaleString() }} files were converted.
            Use Check status below to confirm they no longer count.
          </p>
          <ul
            v-if="allResult.Problems.length"
            class="mt-2 max-h-40 divide-y overflow-y-auto rounded-lg border"
          >
            <li
              v-for="problem in allResult.Problems"
              :key="problem.MediaKey"
              class="px-2.5 py-1.5"
            >
              <span class="text-foreground">{{ problem.FileName }}</span>
              <span class="text-muted-foreground"> · {{ problem.Message }}</span>
            </li>
          </ul>
        </div>
      </template>

      <!-- Earlier conversions -->
      <div
        v-if="recordedCount > 0"
        class="mt-3 rounded-lg border p-3 text-xs"
      >
        <div class="flex items-center gap-2">
          <p class="min-w-0 flex-1 leading-snug">
            <span class="font-medium">{{ recordedCount }} earlier conversion(s) recorded.</span>
            <span class="text-muted-foreground"> Check that they all made it back without counting.</span>
          </p>
          <Button
            variant="outline"
            size="sm"
            class="shrink-0 cursor-pointer"
            :disabled="checking || libraryBusy"
            @click="checkStatus"
          >
            {{ checking ? 'Checking…' : 'Check status' }}
          </Button>
        </div>
        <template v-if="statuses && !checking">
          <p class="mt-2 text-muted-foreground tabular-nums">
            {{ stateCounts.converted }} converted · {{ stateCounts.ready }} waiting to upload
            <template v-if="stateCounts.missing">
              ({{ stateCounts.missing }} without a local copy)
            </template>
            · {{ stateCounts['in-bin'] }} in the bin · {{ stateCounts.counted }} counting again
          </p>
          <ul
            v-if="unfinished.length"
            class="mt-2 max-h-40 divide-y overflow-y-auto rounded-lg border"
          >
            <li
              v-for="status in unfinished"
              :key="status.SHA1Hex"
              class="px-2.5 py-1.5"
            >
              <span class="text-foreground">{{ status.FileName }}</span>
              <span class="text-muted-foreground"> · {{ stateText[status.State] }}</span>
              <span
                v-if="status.LocalMissing"
                class="text-destructive"
              > · the copy on this computer is missing or changed</span>
            </li>
          </ul>
          <div class="mt-2 grid grid-cols-2 gap-2">
            <Button
              size="sm"
              class="cursor-pointer"
              :disabled="stateCounts.ready - stateCounts.missing <= 0 || uploadBusy"
              @click="uploadReady"
            >
              Upload waiting files
            </Button>
            <Button
              variant="outline"
              size="sm"
              class="cursor-pointer"
              :disabled="stateCounts.converted === 0"
              @click="forgetConverted"
            >
              Clear converted
            </Button>
          </div>
        </template>
      </div>

      <!-- Advanced: the step-by-step tools -->
      <details class="mt-3 border-t pt-3">
        <summary class="cursor-pointer text-xs text-muted-foreground select-none hover:text-foreground">
          Advanced: use copies already on this computer, or choose files one by one
        </summary>
        <!-- Warnings: always shown before anything can be changed -->
        <div class="mt-3 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-xs leading-snug">
          <p class="flex items-center gap-1.5 font-semibold text-amber-200">
            <AlertTriangle class="size-3.5 shrink-0" />
            Read before converting
          </p>
          <ul class="mt-1.5 flex list-disc flex-col gap-1 pl-4 text-amber-100/90">
            <li>
              The counted copy is <strong>deleted from Google Photos for good</strong> (not just moved to the bin), then the
              copy from this computer is uploaded again. This cannot be undone.
            </li>
            <li>
              <strong>Lost with the deleted copy:</strong> albums it was in, favourite and archive status, descriptions,
              edits made in Google Photos, sharing links and partner sharing, and people or face tags you corrected.
            </li>
            <li>
              <strong>Kept:</strong> the photo or video itself, and the date and location stored in the file. Files without
              a date in them may show a different date afterwards.
            </li>
            <li>
              Between the delete and the new upload the file exists <strong>only on this computer</strong>. Files are done
              20 at a time; keep the computer on and connected until it finishes.
            </li>
            <li>
              Only files whose copy here is <strong>byte-for-byte identical</strong> are offered, and that copy is checked
              again right before each delete. Live Photos and files that appear more than once (for example in a shared
              album) are left alone.
            </li>
            <li>
              Uploading without counting storage depends on how this unofficial app presents itself to Google. Google can
              change this at any time or act on accounts that use it. You use it at your own risk.
            </li>
          </ul>
        </div>

        <!-- Step 1: folders -->
        <div class="mt-3 border-t pt-3">
          <p class="text-xs font-medium">
            Folders with your original photos and videos
          </p>
          <ul
            v-if="folders.length"
            class="mt-1.5 flex flex-col gap-1"
          >
            <li
              v-for="folder in folders"
              :key="folder"
              class="flex items-center gap-2 rounded-md bg-muted/50 px-2 py-1 text-xs"
            >
              <span
                class="min-w-0 flex-1 truncate font-mono"
                :title="folder"
              >{{ folder }}</span>
              <button
                type="button"
                class="flex shrink-0 cursor-pointer items-center rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                :aria-label="`Remove ${folder}`"
                :disabled="scanning || converting"
                @click="removeFolder(folder)"
              >
                <X class="size-3.5" />
              </button>
            </li>
          </ul>
          <div class="mt-2 grid grid-cols-2 gap-2">
            <Button
              variant="outline"
              size="sm"
              class="cursor-pointer"
              :disabled="scanning || converting"
              @click="addFolder"
            >
              <FolderPlus />
              Add folder
            </Button>
            <Button
              size="sm"
              class="cursor-pointer"
              :disabled="folders.length === 0 || scanning || converting || libraryBusy"
              @click="scan"
            >
              {{ scanning ? 'Searching…' : report ? 'Search again' : 'Find matches' }}
            </Button>
          </div>
          <p class="mt-1.5 text-[11px] leading-snug text-muted-foreground">
            Only files with exactly the size of a counted file are read, so large folders go quickly. Nothing is changed by
            searching.
          </p>
        </div>

        <template v-if="scanning">
          <!-- Two steps: the online library first, then the folders here -->
          <ol class="mt-3 flex flex-col gap-1 border-t pt-3 text-xs">
            <li
              class="flex items-center gap-2"
              :class="scanProgress ? 'text-muted-foreground' : 'font-medium'"
            >
              <span
                class="flex size-4 shrink-0 items-center justify-center rounded-full text-[10px]"
                :class="scanProgress ? 'bg-primary text-primary-foreground' : 'border border-primary text-primary'"
              >{{ scanProgress ? '✓' : '1' }}</span>
              Reading your Google Photos library (online) to list the files that count
            </li>
            <li
              class="flex items-center gap-2"
              :class="scanProgress ? 'font-medium' : 'text-muted-foreground'"
            >
              <span
                class="flex size-4 shrink-0 items-center justify-center rounded-full border text-[10px]"
                :class="scanProgress ? 'border-primary text-primary' : ''"
              >2</span>
              Searching your folders on this computer for identical copies
            </li>
          </ol>
          <LibraryScanLive
            v-if="!scanProgress"
            class="mt-2"
          />
          <div
            v-else
            class="mt-2 rounded-lg border border-primary/25 bg-primary/5 p-3 text-xs"
          >
            <div class="grid grid-cols-3 gap-1.5 tabular-nums">
              <div>
                <p class="text-[10px] tracking-wide text-muted-foreground uppercase">
                  Media files
                </p>
                <p class="mt-0.5 text-sm font-medium">
                  {{ scanProgress.Seen.toLocaleString() }}
                </p>
              </div>
              <div>
                <p class="text-[10px] tracking-wide text-muted-foreground uppercase">
                  Same size, compared
                </p>
                <p class="mt-0.5 text-sm font-medium">
                  {{ scanProgress.Hashed.toLocaleString() }}
                </p>
              </div>
              <div>
                <p class="text-[10px] tracking-wide text-muted-foreground uppercase">
                  Identical
                </p>
                <p class="mt-0.5 text-sm font-medium text-primary">
                  {{ scanProgress.Matched.toLocaleString() }}
                </p>
              </div>
            </div>
            <p
              class="mt-2 truncate font-mono text-[10px] text-muted-foreground"
              :title="scanProgress.Folder"
            >
              {{ scanProgress.Folder }}
            </p>
          </div>
        </template>

        <!-- Step 2: report -->
        <template v-else-if="report">
          <div class="mt-3 grid grid-cols-3 gap-1.5 border-t pt-3">
            <div class="rounded-lg bg-muted/50 px-2.5 py-2">
              <p class="text-[10px] tracking-wide text-muted-foreground uppercase">
                Counting in Photos
              </p>
              <p class="mt-0.5 text-sm font-medium tabular-nums">
                {{ formatBytes(report.CountedBytes, 2) }}
              </p>
              <p class="text-[11px] text-muted-foreground tabular-nums">
                {{ report.CountedFiles.toLocaleString() }} files
              </p>
            </div>
            <div class="rounded-lg bg-primary/10 px-2.5 py-2">
              <p class="text-[10px] tracking-wide text-primary uppercase">
                Can be freed
              </p>
              <p class="mt-0.5 text-sm font-medium tabular-nums">
                {{ formatBytes(report.ConvertibleBytes, 2) }}
              </p>
              <p class="text-[11px] text-muted-foreground tabular-nums">
                {{ items.length.toLocaleString() }} files here
              </p>
            </div>
            <div class="rounded-lg bg-muted/50 px-2.5 py-2">
              <p class="text-[10px] tracking-wide text-muted-foreground uppercase">
                No identical copy
              </p>
              <p class="mt-0.5 text-sm font-medium tabular-nums">
                {{ formatBytes(report.NotOnComputerBytes, 2) }}
              </p>
              <p class="text-[11px] text-muted-foreground tabular-nums">
                {{ report.NotOnComputer.toLocaleString() }} files
              </p>
            </div>
          </div>
          <p class="mt-1.5 text-[11px] leading-snug text-muted-foreground tabular-nums">
            Searched {{ report.ScannedFiles.toLocaleString() }} media files in
            <span class="font-mono">{{ reportFolders.join(', ') }}</span>.
            {{ (report.SizeMatched ?? 0).toLocaleString() }} had exactly the size of a counted file and were compared;
            {{ (items.length + excluded.length).toLocaleString() }} were identical.
          </p>

          <div
            v-if="report.SameNameDifferent"
            class="mt-2 rounded-lg border bg-muted/40 p-2.5 text-[11px] leading-snug"
          >
            <p>
              <span class="font-medium">{{ report.SameNameDifferent.toLocaleString() }} counted files
                ({{ formatBytes(report.SameNameDifferentBytes ?? 0, 2) }}) have a file with the same name here, but a different
                version.</span>
              <span class="text-muted-foreground">
                Usually it was edited, converted (for example HEIC to JPG), compressed, or re-saved with new dates or
                tags after it was uploaded. Google only recognises the identical file, so these can't be converted from
                these folders.
              </span>
            </p>
            <details class="mt-1.5 text-muted-foreground">
              <summary class="cursor-pointer select-none hover:text-foreground">
                Examples
              </summary>
              <ul class="mt-1.5 max-h-36 divide-y overflow-y-auto rounded-lg border bg-background/40">
                <li
                  v-for="example in report.SameNameExamples ?? []"
                  :key="example.LocalPath"
                  class="px-2.5 py-1.5"
                >
                  <span class="text-foreground">{{ example.FileName }}</span>
                  · in Photos {{ formatBytes(example.RemoteSize) }}, here {{ formatBytes(example.LocalSize) }}
                  <span
                    class="block truncate font-mono text-[10px]"
                    :title="example.LocalPath"
                  >{{ example.LocalPath }}</span>
                </li>
              </ul>
            </details>
          </div>

          <details
            v-if="excluded.length"
            class="mt-2 text-[11px] leading-snug text-muted-foreground"
          >
            <summary class="cursor-pointer select-none hover:text-foreground">
              {{ excluded.length }} file(s) found here but left alone
            </summary>
            <ul class="mt-1.5 max-h-32 divide-y overflow-y-auto rounded-lg border">
              <li
                v-for="item in excluded"
                :key="item.MediaKey"
                class="px-2.5 py-1.5"
              >
                <span class="text-foreground">{{ item.FileName }}</span> · {{ item.Reason }}
              </li>
            </ul>
          </details>

          <!-- Counted files with no copy here: download the exact originals first -->
          <div
            v-if="canDownload && (downloadable.length || notDownloadable.length)"
            class="mt-3 rounded-lg border p-3 text-xs"
          >
            <p class="font-medium">
              Not on this computer? Download the originals first
            </p>
            <p class="mt-1 leading-snug text-muted-foreground">
              <template v-if="downloadable.length">
                {{ downloadable.length.toLocaleString() }} counted files ({{ formatBytes(report.DownloadableBytes ?? 0, 2) }})
                can be downloaded from Google Photos as their exact originals. Each download is checked against Google's
                fingerprint of the original and saved in a folder you choose, one subfolder per year, with its file date set
                to when it was taken. Nothing in Google Photos changes. Afterwards they are listed as ready to convert, and
                the folder keeps them as your backup.
              </template>
              <template v-else>
                None of the counted files without a copy here can be downloaded as an exact original.
              </template>
            </p>
            <ul
              v-if="notDownloadable.length"
              class="mt-2 flex flex-col gap-0.5 text-[11px] text-muted-foreground"
            >
              <li
                v-for="reason in notDownloadable"
                :key="reason.Reason"
              >
                {{ reason.Files.toLocaleString() }} ({{ formatBytes(reason.Bytes) }}) left out: {{ reason.Reason }}
              </li>
            </ul>

            <template v-if="downloadable.length">
              <div class="mt-2.5 flex items-center gap-2">
                <span
                  class="min-w-0 flex-1 truncate rounded-md bg-muted/50 px-2 py-1 font-mono"
                  :class="!downloadFolder && 'text-muted-foreground'"
                  :title="downloadFolder"
                >{{ downloadFolder || 'No folder chosen' }}</span>
                <Button
                  variant="outline"
                  size="sm"
                  class="shrink-0 cursor-pointer"
                  :disabled="downloading || converting"
                  @click="chooseDownloadFolder"
                >
                  Choose folder
                </Button>
              </div>
              <p class="mt-1 text-[11px] text-muted-foreground">
                Needs about {{ formatBytes(downloadableSize, 1) }} of free space there.
              </p>

              <div
                v-if="downloading"
                class="mt-2.5 rounded-lg border border-primary/25 bg-primary/5 p-2.5"
              >
                <div class="flex items-center gap-2">
                  <span class="min-w-0 flex-1 truncate font-medium">
                    {{ downloadProgress ? `Downloading ${downloadProgress.Index} of ${downloadProgress.Total} · ${downloadProgress.FileName}` : 'Starting…' }}
                  </span>
                  <span
                    v-if="downloadProgress"
                    class="shrink-0 text-muted-foreground tabular-nums"
                  >{{ formatBytes(downloadProgress.DoneBytes + downloadProgress.BytesDone) }} / {{ formatBytes(downloadTotalBytes) }}</span>
                </div>
                <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-primary/15">
                  <div
                    class="h-full rounded-full bg-primary transition-[width] duration-300"
                    :style="{ width: `${downloadProgress && downloadTotalBytes ? Math.min(100, ((downloadProgress.DoneBytes + downloadProgress.BytesDone) / downloadTotalBytes) * 100) : 0}%` }"
                  />
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  class="mt-2 w-full cursor-pointer"
                  @click="stop"
                >
                  Stop after the current file
                </Button>
              </div>
              <Button
                v-else
                size="sm"
                class="mt-2.5 w-full cursor-pointer"
                :disabled="!downloadFolder || converting || libraryBusy"
                @click="downloadOriginals"
              >
                Download {{ downloadable.length.toLocaleString() }} original(s) · {{ formatBytes(downloadableSize, 1) }}
              </Button>
            </template>

            <ul
              v-if="downloadProblems.length && !downloading"
              class="mt-2 max-h-32 divide-y overflow-y-auto rounded-lg border"
            >
              <li
                v-for="outcome in downloadProblems"
                :key="outcome.MediaKey"
                class="px-2.5 py-1.5"
              >
                <span class="text-foreground">{{ outcome.FileName }}</span>
                <span class="text-muted-foreground"> · {{ outcome.Message }}</span>
              </li>
            </ul>
          </div>

          <template v-if="items.length">
            <!-- By folder -->
            <p class="mt-3 text-xs font-medium">
              By folder
            </p>
            <ul class="mt-1.5 max-h-36 divide-y overflow-y-auto rounded-lg border">
              <li
                v-for="total in folderTotals"
                :key="total.Folder"
              >
                <label class="flex cursor-pointer items-center gap-2 px-2.5 py-1.5 text-xs hover:bg-accent/40">
                  <input
                    type="checkbox"
                    class="accent-primary"
                    :checked="folderSelected(total.Folder)"
                    :disabled="converting"
                    @change="toggleFolder(total.Folder)"
                  >
                  <span
                    class="min-w-0 flex-1 truncate font-mono"
                    :title="total.Folder"
                  >{{ shortFolder(total.Folder) }}</span>
                  <span class="shrink-0 text-muted-foreground tabular-nums">
                    {{ total.Files }} · {{ formatBytes(total.Bytes) }}
                  </span>
                </label>
              </li>
            </ul>

            <!-- Files -->
            <div class="mt-3 flex items-center gap-2 text-xs">
              <span class="font-medium tabular-nums">{{ selectedItems.length }} selected · frees {{ formatBytes(selectedBytes, 2) }}</span>
              <button
                type="button"
                class="ml-auto cursor-pointer rounded px-1.5 py-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                :disabled="converting"
                @click="setAll(selectedItems.length !== items.length)"
              >
                {{ selectedItems.length === items.length ? 'Select none' : 'Select all' }}
              </button>
            </div>
            <ul class="mt-1.5 max-h-56 divide-y overflow-y-auto rounded-lg border">
              <li
                v-for="item in items"
                :key="item.MediaKey"
              >
                <label class="flex cursor-pointer items-start gap-2 px-2.5 py-1.5 text-xs hover:bg-accent/40">
                  <input
                    type="checkbox"
                    class="mt-0.5 accent-primary"
                    :checked="selected.has(item.MediaKey)"
                    :disabled="converting"
                    @change="toggle(item.MediaKey)"
                  >
                  <span class="min-w-0 flex-1 leading-snug">
                    <span class="flex items-center gap-1.5">
                      <span class="min-w-0 truncate">{{ item.FileName }}</span>
                      <span class="shrink-0 text-muted-foreground tabular-nums">· {{ formatBytes(item.QuotaBytes) }}</span>
                      <span
                        v-if="item.Favorite"
                        class="shrink-0 rounded bg-amber-500/15 px-1 text-[10px] text-amber-200"
                      >favourite</span>
                      <span
                        v-if="item.Edited"
                        class="shrink-0 rounded bg-amber-500/15 px-1 text-[10px] text-amber-200"
                      >edited in Photos</span>
                      <span
                        v-if="item.Archived"
                        class="shrink-0 rounded bg-muted px-1 text-[10px] text-muted-foreground"
                      >archived</span>
                      <button
                        type="button"
                        class="ml-auto flex shrink-0 cursor-pointer items-center rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                        title="Open in Google Photos (see its albums before converting)"
                        :aria-label="`Open ${item.FileName} in Google Photos`"
                        @click.stop.prevent="openInGooglePhotos(item.MediaKey)"
                      >
                        <ExternalLink class="size-3" />
                      </button>
                    </span>
                    <span
                      class="block truncate font-mono text-[10px] text-muted-foreground"
                      :title="item.LocalPath"
                    >{{ item.LocalPath }}</span>
                  </span>
                </label>
              </li>
            </ul>

            <!-- Step 3: confirm and convert -->
            <div
              v-if="!converting"
              class="mt-3 rounded-lg border border-destructive/40 bg-destructive/5 p-3"
            >
              <p
                v-if="selectedLosses.favorite || selectedLosses.edited || selectedLosses.archived"
                class="mb-2 text-xs leading-snug text-amber-200"
              >
                Of the selected files, {{ selectedLosses.favorite }} are favourites, {{ selectedLosses.edited }} have edits made
                in Google Photos and {{ selectedLosses.archived }} are archived. That is lost. Album membership is not shown
                here; open a file to check its albums.
              </p>
              <label class="flex cursor-pointer items-start gap-2 text-xs leading-snug select-none">
                <input
                  v-model="acknowledged"
                  type="checkbox"
                  class="mt-0.5 accent-destructive"
                  :disabled="selectedItems.length === 0"
                >
                <span>
                  I understand that the {{ selectedItems.length }} selected file(s) are deleted from Google Photos for good and
                  uploaded again from this computer, and that their albums, favourites, descriptions, edits and sharing are lost.
                </span>
              </label>
              <Button
                variant="destructive"
                size="sm"
                class="mt-2.5 w-full cursor-pointer"
                :disabled="!acknowledged || selectedItems.length === 0 || uploadBusy"
                @click="convert"
              >
                {{ uploadBusy ? 'Wait for the current upload to finish' : `Convert ${selectedItems.length} file(s) · free ${formatBytes(selectedBytes, 2)}` }}
              </Button>
            </div>
          </template>
          <p
            v-else
            class="mt-3 text-xs leading-snug text-muted-foreground"
          >
            None of the counted files has an identical copy in these folders yet. Download the originals above, or add a
            folder where you keep them (another drive, a phone backup) and search again.
          </p>
        </template>

        <!-- Running -->
        <div
          v-if="converting"
          class="mt-3 rounded-lg border border-primary/30 bg-primary/5 p-3 text-xs"
        >
          <div class="flex items-center gap-2">
            <span class="font-medium">
              <template v-if="!progress">Starting…</template>
              <template v-else-if="progress.Phase === 'deleting'">
                Deleting counted copies · batch {{ progress.Batch }} of {{ progress.Batches }}
              </template>
              <template v-else>
                Uploading batch {{ progress.Batch }} of {{ progress.Batches }} without quota (progress on the main screen)
              </template>
            </span>
            <span
              v-if="progress"
              class="ml-auto text-muted-foreground tabular-nums"
            >{{ progress.Done }} / {{ progress.Total }}</span>
          </div>
          <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-primary/15">
            <div
              class="h-full rounded-full bg-primary transition-[width] duration-500"
              :style="{ width: `${progress && progress.Total ? (progress.Done / progress.Total) * 100 : 0}%` }"
            />
          </div>
          <Button
            variant="outline"
            size="sm"
            class="mt-2.5 w-full cursor-pointer"
            @click="stop"
          >
            Stop after the current file
          </Button>
          <p class="mt-1.5 text-[11px] leading-snug text-muted-foreground">
            Files already deleted are still uploaded again before it stops.
          </p>
        </div>

        <ul
          v-if="problemOutcomes.length"
          class="mt-3 max-h-40 divide-y overflow-y-auto rounded-lg border text-xs"
        >
          <li
            v-for="outcome in problemOutcomes"
            :key="outcome.MediaKey"
            class="flex gap-2 px-2.5 py-1.5"
          >
            <SkipForward
              v-if="outcome.Status === 'skipped'"
              class="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
            />
            <CircleX
              v-else
              class="mt-0.5 size-3.5 shrink-0 text-destructive"
            />
            <span class="min-w-0 flex-1 leading-snug">
              {{ outcome.FileName }} <span class="text-muted-foreground">· {{ outcome.Message }}</span>
            </span>
          </li>
        </ul>
        <p
          v-else-if="outcomes.length && !converting"
          class="mt-3 flex items-center gap-1.5 text-xs text-primary"
        >
          <CircleCheck class="size-3.5" />
          All {{ outcomes.length }} counted copies were deleted and uploaded again.
        </p>
      </details>
    </div>
  </section>
</template>
