import { reactive } from 'vue'
import { Events } from '@wailsio/runtime'
import * as guiBindings from '../../bindings/app/internal/gui'
import { debugLog } from './debug'

export interface Album {
  MediaKey: string
  Title: string
  ItemCount: number
  Type: number
  CoverMediaKey: string
  LastActivity: number
}
interface AlbumList {
  Albums: Album[] | null
  SyncedAt: number
  Known: boolean
}

// Reached by name so older bindings still build.
const api = (guiBindings as unknown as {
  LibraryService?: { CachedAlbums?: () => Promise<AlbumList>, RefreshAlbums?: () => Promise<AlbumList> }
}).LibraryService

/** A background refresh newer than this is not repeated. */
const FRESH_MS = 2 * 60 * 1000

/**
 * The account's albums, fetched in the background when the app starts so the
 * album picker opens with them already there.
 */
export const albumStore = reactive({
  albums: [] as Album[],
  /** False until the library has been read once with albums. */
  known: false,
  /** Last library read, Unix ms. */
  syncedAt: 0,
  refreshing: false,
  /** Items read so far while the first full read runs. */
  itemsRead: 0,
  available: Boolean(api?.CachedAlbums),
})

function apply(list: AlbumList) {
  albumStore.albums = list.Albums ?? []
  albumStore.known = list.Known
  albumStore.syncedAt = list.SyncedAt * 1000
}

Events.On('librarySyncProgress', (event: { data: { Items?: number } }) => {
  if (albumStore.refreshing) albumStore.itemsRead = event.data?.Items ?? 0
})

/** Shows the albums saved by the last library read; never goes online. */
export async function loadCachedAlbums() {
  if (!api?.CachedAlbums) return
  try {
    apply(await api.CachedAlbums())
  } catch (error) {
    debugLog('error', 'frontend', 'CachedAlbums failed', String(error))
  }
}

/**
 * Fetches album changes in the background. The first time it reads the whole
 * library (a few minutes); afterwards only what changed (seconds).
 */
export async function refreshAlbums(force = false) {
  if (!api?.RefreshAlbums || albumStore.refreshing) return
  if (!force && albumStore.known && Date.now() - albumStore.syncedAt < FRESH_MS) return
  albumStore.refreshing = true
  albumStore.itemsRead = 0
  try {
    apply(await api.RefreshAlbums())
  } catch (error) {
    debugLog('warn', 'frontend', 'RefreshAlbums failed', String(error))
  } finally {
    albumStore.refreshing = false
  }
}

/** Called at start and on account switch: cached list now, fresh one after. */
export async function prefetchAlbums() {
  await loadCachedAlbums()
  void refreshAlbums()
}

const RECENT_KEY = 'gotohp.recentAlbums'
const RECENT_MAX = 5

function readRecent(): string[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]')
    return Array.isArray(parsed) ? parsed.filter(value => typeof value === 'string').slice(0, RECENT_MAX) : []
  } catch {
    return []
  }
}

/** Album keys picked recently, newest first. */
export const recentAlbumKeys = reactive({ keys: readRecent() })

/** Remembers an album picked for an upload, so the picker lists it first. */
export function rememberAlbum(key: string) {
  if (!key.startsWith('AF1Qip')) return
  recentAlbumKeys.keys = [key, ...recentAlbumKeys.keys.filter(existing => existing !== key)].slice(0, RECENT_MAX)
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(recentAlbumKeys.keys))
  } catch {
    // Without storage the list lasts for this session.
  }
}

/** The album's title from the last list, or '' when unknown. */
export function albumTitle(key: string): string {
  return albumStore.albums.find(album => album.MediaKey === key)?.Title ?? ''
}
