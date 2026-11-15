import { computed, reactive, ref, watch } from 'vue'
import { Browser } from '@wailsio/runtime'
import { toast } from 'vue-sonner'
import * as guiBindings from '../../bindings/app/internal/gui'
import { debugLog } from './debug'

/** The running build, as the backend reports it. */
export interface AppInfo {
  Version: string
  Commit: string
  BuildDate: string
  Repo: string
}

export interface ReleaseAsset {
  Name: string
  URL: string
  Size: number
}

/** Result of asking GitHub for the latest release. */
export interface UpdateInfo {
  Current: string
  /** Empty when the repository has no releases yet. */
  Latest: string
  Newer: boolean
  Name: string
  Notes: string
  URL: string
  PublishedAt: number
  CheckedAt: number
  Assets: ReleaseAsset[] | null
}

interface AppServiceApi {
  Info: () => Promise<AppInfo>
  Changelog: () => Promise<string>
  CheckForUpdate: () => Promise<UpdateInfo>
}

// Reached by name, so the app also runs with bindings generated before
// AppService existed (the About section then stays empty).
const service = (guiBindings as unknown as { AppService?: AppServiceApi }).AppService

const AUTO_KEY = 'gotohp.updates.auto'
const LAST_KEY = 'gotohp.updates.lastCheck'
const DISMISSED_KEY = 'gotohp.updates.dismissed'
const RESULT_KEY = 'gotohp.updates.result'
/** Automatic checks run at most this often. */
const AUTO_INTERVAL_MS = 24 * 60 * 60 * 1000

function readStorage(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStorage(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Without storage the value lasts for this session.
  }
}

/** Check GitHub for a new release on start, at most once a day. On by default. */
export const autoCheckUpdates = ref(readStorage(AUTO_KEY) !== 'false')
watch(autoCheckUpdates, enabled => writeStorage(AUTO_KEY, String(enabled)))

/** The last check's result, so the banner survives a restart between daily checks. */
function readCachedResult(): UpdateInfo | null {
  try {
    const parsed = JSON.parse(readStorage(RESULT_KEY) ?? 'null') as UpdateInfo | null
    return parsed && typeof parsed.Latest === 'string' ? parsed : null
  } catch {
    return null
  }
}

/** Splits "v2.1.0-rc1" into [2, 1, 0]; null when it is not a version. */
export function parseVersion(version: string): [number, number, number] | null {
  const core = version.trim().replace(/^v/, '').split(/[-+]/)[0] ?? ''
  if (!/^\d+(\.\d+){0,2}$/.test(core)) return null
  const [major = 0, minor = 0, patch = 0] = core.split('.').map(Number)
  return [major, minor, patch]
}

/** 1 when a is newer than b, -1 when older, 0 when equal or unreadable. */
export function compareVersions(a: string, b: string): number {
  const left = parseVersion(a)
  const right = parseVersion(b)
  if (!left || !right) return left ? 1 : right ? -1 : 0
  for (let i = 0; i < 3; i++) {
    if (left[i] !== right[i]) return left[i]! > right[i]! ? 1 : -1
  }
  return 0
}

export const appUpdate = reactive({
  info: null as AppInfo | null,
  update: readCachedResult(),
  checking: false,
  error: '',
  /** Unix ms of the last successful check, kept between sessions. */
  lastCheckedAt: Number(readStorage(LAST_KEY)) || 0,
  /** Version whose banner was closed; it stays hidden until a newer one appears. */
  dismissed: readStorage(DISMISSED_KEY) ?? '',
})

/** Shows the update banner for a newer release that was not dismissed. */
export const updateBannerVisible = computed(() =>
  // Waits for the build's version, so a cached result is never shown stale.
  Boolean(appUpdate.info && appUpdate.update?.Newer && appUpdate.update.Latest !== appUpdate.dismissed),
)

export function hasAppService(): boolean {
  return service !== undefined
}

export async function loadAppInfo(): Promise<AppInfo | null> {
  if (appUpdate.info || !service) return appUpdate.info
  try {
    appUpdate.info = await service.Info()
    // A cached result is only an update if it is still newer than this build.
    if (appUpdate.update && appUpdate.info) {
      appUpdate.update.Current = appUpdate.info.Version
      appUpdate.update.Newer = compareVersions(appUpdate.update.Latest, appUpdate.info.Version) > 0
    }
  } catch (error) {
    debugLog('error', 'frontend', 'AppService.Info failed', String(error))
  }
  return appUpdate.info
}

export async function loadChangelog(): Promise<string> {
  if (!service) return ''
  try {
    return await service.Changelog()
  } catch (error) {
    debugLog('error', 'frontend', 'AppService.Changelog failed', String(error))
    return ''
  }
}

/**
 * Asks GitHub for the latest release. A manual check reports its result in a
 * toast; an automatic one stays quiet unless there is an update.
 */
export async function checkForUpdates(manual = false): Promise<UpdateInfo | null> {
  if (!service || appUpdate.checking) return appUpdate.update
  appUpdate.checking = true
  appUpdate.error = ''
  try {
    const update = await service.CheckForUpdate()
    appUpdate.update = update
    appUpdate.lastCheckedAt = update.CheckedAt || Date.now()
    writeStorage(LAST_KEY, String(appUpdate.lastCheckedAt))
    writeStorage(RESULT_KEY, JSON.stringify(update))
    debugLog('info', 'frontend', 'update check', { current: update.Current, latest: update.Latest, newer: update.Newer })
    if (manual) {
      if (update.Newer) {
        // A manual check shows the banner again even if it was closed before.
        appUpdate.dismissed = ''
        writeStorage(DISMISSED_KEY, '')
      } else if (!update.Latest) {
        toast.info('No releases published yet.')
      } else {
        toast.success(`You're up to date (version ${update.Current || update.Latest}).`)
      }
    }
    return update
  } catch (error) {
    appUpdate.error = error instanceof Error ? error.message : String(error)
    debugLog('warn', 'frontend', 'update check failed', appUpdate.error)
    if (manual) toast.error('Could not check for updates', { description: appUpdate.error })
    return null
  } finally {
    appUpdate.checking = false
  }
}

/** Runs the daily check on start when automatic checks are on. */
export async function maybeAutoCheckForUpdates() {
  await loadAppInfo()
  if (!autoCheckUpdates.value) return
  if (Date.now() - appUpdate.lastCheckedAt < AUTO_INTERVAL_MS) return
  await checkForUpdates(false)
}

export function dismissUpdate() {
  const latest = appUpdate.update?.Latest ?? ''
  appUpdate.dismissed = latest
  writeStorage(DISMISSED_KEY, latest)
}

export async function openExternal(url: string) {
  if (!url) return
  try {
    await Browser.OpenURL(url)
  } catch (error) {
    toast.error('Could not open the browser', {
      description: error instanceof Error ? error.message : String(error),
    })
  }
}

/** The release page of the newest version, or the repository's releases. */
export function releasePageUrl(): string {
  const repo = appUpdate.info?.Repo || 'emmanuelmavely/gotohp'
  return appUpdate.update?.URL || `https://github.com/${repo}/releases/latest`
}
