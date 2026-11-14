import { Events } from '@wailsio/runtime'
import { reactive, ref } from 'vue'
import { LibraryService } from '../../bindings/app/internal/gui'
import { AccountStorage } from '../../bindings/app/core'
import { debugLog } from './debug'

/** Ticks once a minute so "updated 5 min ago" labels stay current. */
export const minuteClock = ref(Date.now())
setInterval(() => minuteClock.value = Date.now(), 60_000)

const STORAGE_RESULT_KEY = 'gotohp.accountStorage.'

/** The last storage count of an account, kept between sessions. */
function readStoredResult(account: string): AccountStorage | null {
  try {
    const raw = localStorage.getItem(STORAGE_RESULT_KEY + account)
    if (!raw) return null
    const parsed = JSON.parse(raw)
    if (typeof parsed?.Summary?.TotalBytes !== 'number') return null
    return AccountStorage.createFrom(parsed)
  } catch {
    return null
  }
}

function storeResult(account: string, data: AccountStorage) {
  try {
    localStorage.setItem(STORAGE_RESULT_KEY + account, JSON.stringify(data))
  } catch {
    // Without storage the count lasts for this session.
  }
}

/** When the account's storage was last counted, in milliseconds, or 0. */
export function storageCountedAt(data: AccountStorage | null): number {
  return data && data.SyncedAt > 0 ? data.SyncedAt * 1000 : 0
}

/** Running totals the backend sends after every page of a library scan. */
export interface LibraryScanProgress {
  /** Pages fetched so far; 0 while only changes since the last scan are checked. */
  Pages: number
  Items: number
  TotalBytes: number
  ChargedItems: number
  ChargedBytes: number
  /** Each stored file counted once. Missing on backends built before live progress. */
  UniqueChargedItems?: number
  UniqueChargedBytes?: number
}

/**
 * Any library scan in flight: storage count or Live Photo scan. Both walk the
 * same library, so they share one live view.
 */
export const libraryScan = reactive({
  progress: null as LibraryScanProgress | null,
  /** When the current scan was started from this window, 0 when idle. */
  startedAt: 0,
  /** Items found by the last complete scan of this account, 0 when unknown. */
  expectedItems: 0,
  /** A Live Photo repair is running; it syncs the library too. */
  repairing: false,
})

/**
 * Storage the selected account really uses, as counted from the remote library.
 * Shared by the strip under the account name and the statistics panel.
 */
export const accountStorage = reactive({
  /** Account the numbers below belong to. */
  account: '',
  data: null as AccountStorage | null,
  loading: false,
  error: '',
})

const EXPECTED_ITEMS_KEY = 'gotohp.libraryItems.'

function readExpectedItems(account: string): number {
  try {
    return Number(localStorage.getItem(EXPECTED_ITEMS_KEY + account)) || 0
  } catch {
    return 0
  }
}

function rememberExpectedItems(account: string, items: number) {
  libraryScan.expectedItems = items
  try {
    localStorage.setItem(EXPECTED_ITEMS_KEY + account, String(items))
  } catch {
    // Without storage the next scan simply has no estimate.
  }
}

Events.On('librarySyncProgress', (event: { data: LibraryScanProgress }) => {
  debugLog('event', 'backend', 'librarySyncProgress', event.data)
  // Ignore stragglers from a scan this window is no longer waiting for.
  if (libraryScan.startedAt > 0) libraryScan.progress = event.data
})

/** Marks a library scan as running so its progress events are shown. */
export function beginLibraryScan() {
  libraryScan.progress = null
  libraryScan.startedAt = Date.now()
}

/** Ends the live view; pass the final item count of a completed scan to remember it. */
export function endLibraryScan(finalItems?: number) {
  libraryScan.progress = null
  libraryScan.startedAt = 0
  if (finalItems && finalItems > 0 && accountStorage.account) {
    rememberExpectedItems(accountStorage.account, finalItems)
  }
}

let requestId = 0

/** Forgets the numbers when the selected account changes. */
export function resetAccountStorage(account: string) {
  if (accountStorage.account === account) return
  requestId++ // a result still on its way belongs to the previous account
  accountStorage.account = account
  // Show the last count right away; Refresh counts again.
  accountStorage.data = account ? readStoredResult(account) : null
  accountStorage.loading = false
  accountStorage.error = ''
  libraryScan.progress = null
  libraryScan.startedAt = 0
  libraryScan.expectedItems = account ? readExpectedItems(account) : 0
}

/**
 * Syncs the library and counts the storage in use. The first run for an account
 * walks the whole library and can take minutes; later runs only fetch changes.
 * Returns false when the calculation failed.
 */
export async function calculateAccountStorage(): Promise<boolean> {
  // One library scan at a time; a Live Photo scan may already be running.
  if (accountStorage.loading || libraryScan.startedAt > 0 || libraryScan.repairing) return true
  const id = ++requestId
  accountStorage.loading = true
  accountStorage.error = ''
  beginLibraryScan()
  debugLog('info', 'frontend', 'AccountStorage requested', { account: accountStorage.account })
  try {
    const data = await LibraryService.AccountStorage()
    if (id !== requestId) return true
    accountStorage.data = data
    storeResult(accountStorage.account, data)
    endLibraryScan(data.Items)
    return true
  } catch (error) {
    if (id !== requestId) return true
    accountStorage.error = error instanceof Error ? error.message : String(error)
    debugLog('error', 'frontend', 'AccountStorage failed', accountStorage.error)
    endLibraryScan()
    return false
  } finally {
    if (id === requestId) accountStorage.loading = false
  }
}
