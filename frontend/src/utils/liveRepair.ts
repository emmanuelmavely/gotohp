import { Events } from '@wailsio/runtime'
import { reactive } from 'vue'
import { LibraryService } from '../../bindings/app/internal/gui'
import type { LivePhotoRepairOutcome } from '../../bindings/app/core'
import { libraryScan } from './accountStorage'
import { debugLog } from './debug'

/** Stages one pair goes through, in order, as reported by the backend. */
export const REPAIR_STAGES = ['downloading', 'verifying', 'uploading', 'linking', 'trashing'] as const

/** Where a batch of Live Photo repairs stands, sent at every stage change. */
export interface LivePhotoRepairProgress {
  /** 1-based position of the current pair. */
  Index: number
  Total: number
  Stem: string
  /** The separate video being joined to its photo. */
  FileName: string
  Stage: string
  BytesDone: number
  BytesTotal: number
  /** Set once the pair is finished (Stage "done"). */
  Outcome?: LivePhotoRepairOutcome | null
}

/**
 * The running repair batch. Kept outside the statistics sheet so closing the
 * sheet does not lose track of a repair that keeps going in the background.
 */
export const liveRepair = reactive({
  running: false,
  cancelling: false,
  startedAt: 0,
  progress: null as LivePhotoRepairProgress | null,
  /** Results so far, in the order the pairs finished. */
  outcomes: [] as LivePhotoRepairOutcome[],
})

Events.On('livePhotoRepairProgress', (event: { data: LivePhotoRepairProgress }) => {
  if (!liveRepair.running) return
  const progress = event.data
  if (progress.Stage === 'done') {
    debugLog('event', 'backend', 'livePhotoRepairProgress', progress)
    if (progress.Outcome) liveRepair.outcomes.push(progress.Outcome)
  }
  liveRepair.progress = progress
})

/** Share of the whole batch done, 0..1, from the pair index and its stage. */
export function repairFraction(progress: LivePhotoRepairProgress | null): number {
  if (!progress || progress.Total <= 0) return 0
  const bytes = progress.BytesTotal > 0 ? Math.min(1, progress.BytesDone / progress.BytesTotal) : 0
  const withinPair: Record<string, number> = {
    downloading: 0.45 * bytes,
    verifying: 0.47,
    uploading: 0.5 + 0.43 * bytes,
    linking: 0.95,
    trashing: 0.98,
    done: 1,
  }
  return Math.min(1, (progress.Index - 1 + (withinPair[progress.Stage] ?? 0)) / progress.Total)
}

/**
 * Repairs the given pairs one at a time. Resolves with every outcome, including
 * pairs that were not attempted after a cancel or a lost connection.
 */
export async function runLivePhotoRepair(stems: string[]): Promise<LivePhotoRepairOutcome[]> {
  // The backend refuses an empty list too; never ask it to repair "everything".
  if (stems.length === 0 || liveRepair.running) return []
  liveRepair.running = true
  liveRepair.cancelling = false
  liveRepair.startedAt = Date.now()
  liveRepair.progress = null
  liveRepair.outcomes = []
  libraryScan.repairing = true
  debugLog('info', 'frontend', 'RepairLivePhotos', { stems })
  try {
    const outcomes = await LibraryService.RepairLivePhotos(stems)
    liveRepair.outcomes = outcomes
    return outcomes
  } finally {
    liveRepair.running = false
    liveRepair.cancelling = false
    liveRepair.progress = null
    libraryScan.repairing = false
  }
}

/** Stops the batch after the current step; the rest is reported as skipped. */
export function cancelLivePhotoRepair() {
  if (!liveRepair.running || liveRepair.cancelling) return
  liveRepair.cancelling = true
  debugLog('info', 'frontend', 'livePhotoRepairCancel')
  Events.Emit('livePhotoRepairCancel')
}
