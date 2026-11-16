import { reactive } from 'vue'
import { Events } from '@wailsio/runtime'
import { toast } from 'vue-sonner'
import * as guiBindings from '../../bindings/app/internal/gui'
import { libraryScan } from './accountStorage'

export interface TakeawayAlbum {
  Title: string
  AlbumKey: string
  Count: number
}
export interface TakeawayPlan {
  Items: number
  Albums: number
  Skipped: number
}
export interface TakeawayProgress {
  Album: number
  AlbumTotal: number
  AlbumTitle: string
  Done: number
  Total: number
}
export interface TakeawayResult {
  Albums: TakeawayAlbum[] | null
  Items: number
  Stopped: boolean
}

// Reached by name so older bindings still build.
const api = (guiBindings as unknown as {
  LibraryService?: {
    TakeawayPlan?: (albumSize: number) => Promise<TakeawayPlan>
    PrepareTakeaway?: (albumSize: number, confirmed: boolean) => Promise<TakeawayResult>
  }
}).LibraryService

const SIZE_KEY = 'gotohp.takeawayAlbumSize'
function readSize(): number {
  try {
    return Number(localStorage.getItem(SIZE_KEY)) || 2000
  } catch {
    return 2000
  }
}

/**
 * Album takeaway: groups the selected account's photos into shared albums so
 * they can be shared and saved into another account by hand. Kept outside the
 * page so a run survives navigating away.
 */
export const takeaway = reactive({
  available: Boolean(api?.PrepareTakeaway),
  albumSize: readSize(),
  plan: null as TakeawayPlan | null,
  planning: false,
  running: false,
  stopping: false,
  progress: null as TakeawayProgress | null,
  result: null as TakeawayResult | null,
})

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function setAlbumSize(size: number) {
  takeaway.albumSize = size
  takeaway.plan = null
  try {
    localStorage.setItem(SIZE_KEY, String(size))
  } catch {
    // Without storage the choice lasts for this session.
  }
}

const libraryBusy = () => libraryScan.startedAt > 0 || libraryScan.repairing

/** Reads the selected account's library and reports what it would gather. */
export async function loadTakeawayPlan() {
  if (!api?.TakeawayPlan || takeaway.planning || takeaway.running || libraryBusy()) return
  takeaway.planning = true
  try {
    takeaway.plan = await api.TakeawayPlan(takeaway.albumSize)
  } catch (error) {
    toast.error('Could not read the account', { description: errorText(error) })
  } finally {
    takeaway.planning = false
  }
}

/** Gathers the account's photos into the takeaway albums. */
export async function startTakeaway() {
  if (!api?.PrepareTakeaway || takeaway.running) return
  takeaway.running = true
  takeaway.stopping = false
  takeaway.progress = null
  takeaway.result = null
  try {
    takeaway.result = await api.PrepareTakeaway(takeaway.albumSize, true)
  } catch (error) {
    toast.error('Album takeaway stopped', { description: errorText(error) })
  } finally {
    takeaway.running = false
    takeaway.stopping = false
  }
  await loadTakeawayPlan()
}

/** Stops after the current request; the albums already made are kept. */
export function cancelTakeaway() {
  if (!takeaway.running) return
  takeaway.stopping = true
  Events.Emit('takeawayCancel')
}

Events.On('takeawayProgress', (event: { data: TakeawayProgress }) => {
  if (takeaway.running) takeaway.progress = event.data
})
