import { reactive } from 'vue'
import { Events } from '@wailsio/runtime'
import { toast } from 'vue-sonner'
import * as guiBindings from '../../bindings/app/internal/gui'
import { beginLibraryScan, endLibraryScan, libraryScan } from './accountStorage'
import { debugLog } from './debug'
import { uploadManager } from './UploadManager'

export interface ConvertAllPlan {
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
export interface ConvertProblem {
  MediaKey: string
  FileName: string
  Status: string
  Message: string
}
export interface ConvertAllResult {
  Total: number
  Converted: number
  Problems: ConvertProblem[] | null
}
export interface ConvertProgress {
  Phase: 'downloading' | 'deleting' | 'uploading'
  Done: number
  Total: number
  Batch: number
  Batches: number
  FileName: string
  BytesDone: number
  BytesTotal: number
}
interface ConversionStatus {
  State: 'converted' | 'ready' | 'in-bin' | 'counted'
  LocalMissing: boolean
}

// Reached by name so older bindings still build.
const bindings = guiBindings as unknown as {
  LibraryService?: {
    QuotaConvertAllPlan?: () => Promise<ConvertAllPlan>
    QuotaConvertAll?: (folder: string, limit: number, acknowledged: boolean) => Promise<ConvertAllResult>
    QuotaRecordedCount?: () => Promise<number>
    QuotaConversionStatus?: () => Promise<ConversionStatus[]>
    QuotaUploadReady?: () => Promise<number>
  }
  DialogService?: { PickDownloadFolder?: () => Promise<string> }
}
const api = bindings.LibraryService

const FOLDER_KEY = 'gotohp.quotaDownloadFolder'
function readFolder(): string {
  try {
    return localStorage.getItem(FOLDER_KEY) ?? ''
  } catch {
    return ''
  }
}

const SAMPLES = 120

/**
 * The Storage page's conversion. Kept outside the page so it survives
 * switching pages: a run goes on in the background.
 */
export const quotaConvert = reactive({
  available: Boolean(api?.QuotaConvertAllPlan && api?.QuotaConvertAll),
  plan: null as ConvertAllPlan | null,
  planAt: 0,
  planning: false,
  running: false,
  /** Set after Pause until the batch in flight has finished. */
  pausing: false,
  /** A run was paused; Resume continues it. */
  paused: false,
  /** The run is a trial of a few files. */
  trial: false,
  startedAt: 0,
  progress: null as ConvertProgress | null,
  result: null as ConvertAllResult | null,
  folder: readFolder(),
  /** Download speed samples (bytes/s), one per second. */
  downloadHistory: [] as number[],
  /** Upload speed samples (bytes/s), one per second. */
  uploadHistory: [] as number[],
  recorded: 0,
  /** Earlier conversions confirmed back without counting, from the last check. */
  confirmed: null as null | { converted: number, waiting: number, inBin: number, counting: number },
  checking: false,
})

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export const backupFolder = () => quotaConvert.folder || quotaConvert.plan?.DefaultFolder || ''

export async function chooseBackupFolder() {
  try {
    const folder = await bindings.DialogService?.PickDownloadFolder?.()
    if (!folder) return
    quotaConvert.folder = folder
    try {
      localStorage.setItem(FOLDER_KEY, folder)
    } catch {
      // Without storage the choice lasts for this session.
    }
  } catch (error) {
    debugLog('error', 'frontend', 'PickDownloadFolder failed', errorText(error))
  }
}

/** Reads what counts against storage; the first time it reads the whole library. */
export async function loadPlan() {
  if (!api?.QuotaConvertAllPlan || quotaConvert.planning || quotaConvert.running) return
  if (libraryScan.startedAt > 0 || libraryScan.repairing) return
  quotaConvert.planning = true
  beginLibraryScan()
  try {
    quotaConvert.plan = await api.QuotaConvertAllPlan()
    quotaConvert.planAt = Date.now()
  } catch (error) {
    toast.error('Could not read your storage', { description: errorText(error) })
  } finally {
    endLibraryScan()
    quotaConvert.planning = false
  }
  void loadRecorded()
}

export async function loadRecorded() {
  try {
    quotaConvert.recorded = (await api?.QuotaRecordedCount?.()) ?? 0
  } catch {
    quotaConvert.recorded = 0
  }
}

/** Checks that earlier conversions came back without counting. */
export async function checkEarlier() {
  if (!api?.QuotaConversionStatus || quotaConvert.checking) return
  quotaConvert.checking = true
  try {
    const statuses = await api.QuotaConversionStatus()
    const counts = { converted: 0, waiting: 0, inBin: 0, counting: 0 }
    for (const status of statuses) {
      if (status.State === 'converted') counts.converted++
      else if (status.State === 'ready') counts.waiting++
      else if (status.State === 'in-bin') counts.inBin++
      else counts.counting++
    }
    quotaConvert.confirmed = counts
  } catch (error) {
    toast.error('Could not check earlier conversions', { description: errorText(error) })
  } finally {
    quotaConvert.checking = false
  }
}

export async function uploadWaiting() {
  try {
    const started = (await api?.QuotaUploadReady?.()) ?? 0
    toast.info(started > 0 ? `Uploading ${started} file(s) without counting` : 'Nothing is waiting to upload')
  } catch (error) {
    toast.error('Could not start the upload', { description: errorText(error) })
  }
}

let sampleTimer: ReturnType<typeof setInterval> | null = null
let bytesThisSecond = 0
let lastFile = ''
let lastBytes = 0

function startSampling() {
  quotaConvert.downloadHistory = []
  quotaConvert.uploadHistory = []
  bytesThisSecond = 0
  sampleTimer = setInterval(() => {
    quotaConvert.downloadHistory.push(bytesThisSecond)
    quotaConvert.uploadHistory.push(uploadManager.state.isUploading ? Math.round(uploadManager.state.uploadSpeed) : 0)
    bytesThisSecond = 0
    if (quotaConvert.downloadHistory.length > SAMPLES) quotaConvert.downloadHistory.shift()
    if (quotaConvert.uploadHistory.length > SAMPLES) quotaConvert.uploadHistory.shift()
  }, 1000)
}

function stopSampling() {
  if (sampleTimer !== null) clearInterval(sampleTimer)
  sampleTimer = null
}

/**
 * Converts the counted files 20 at a time. limit > 0 converts only that many
 * (a trial). Running it again continues where it stopped.
 */
export async function startConvert(limit = 0) {
  if (!api?.QuotaConvertAll || quotaConvert.running) return
  quotaConvert.running = true
  quotaConvert.paused = false
  quotaConvert.pausing = false
  quotaConvert.trial = limit > 0
  quotaConvert.progress = null
  quotaConvert.result = null
  quotaConvert.startedAt = Date.now()
  startSampling()
  try {
    quotaConvert.result = await api.QuotaConvertAll(backupFolder(), limit, true)
    // Stopped before the end: the run was paused.
    const result = quotaConvert.result
    if (quotaConvert.pausing && result && result.Converted + (result.Problems?.length ?? 0) < result.Total) {
      quotaConvert.paused = true
    }
  } catch (error) {
    toast.error('Conversion stopped', { description: errorText(error) })
  } finally {
    stopSampling()
    quotaConvert.running = false
    quotaConvert.pausing = false
  }
  void loadRecorded()
  await loadPlan()
}

/** Stops after the batch in flight; Resume continues from there. */
export function pauseConvert() {
  if (!quotaConvert.running) return
  quotaConvert.pausing = true
  Events.Emit('quotaConvertCancel')
}

Events.On('quotaConvertProgress', (event: { data: ConvertProgress }) => {
  if (!quotaConvert.running) return
  const progress = event.data
  if (progress.Phase === 'downloading') {
    if (progress.FileName !== lastFile) {
      lastFile = progress.FileName
      lastBytes = 0
    }
    if (progress.BytesDone > lastBytes) bytesThisSecond += progress.BytesDone - lastBytes
    lastBytes = progress.BytesDone
  }
  quotaConvert.progress = progress
})
