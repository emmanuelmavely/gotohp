<script setup lang="ts">
import { computed, nextTick, ref, onMounted, watch } from 'vue'
import { ConfigManager } from '../bindings/app/core'
import { DialogService } from '../bindings/app/internal/gui'
import { Events } from '@wailsio/runtime'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import Button from '@/components/ui/button/Button.vue'
import SettingRow from '@/components/SettingRow.vue'
import { debugEnabled } from '@/utils/debug'
import { thumbnailsEnabled } from '@/utils/thumbnails'
import { closeBehavior, type CloseBehavior } from '@/utils/closeBehavior'
import NotesSheet from '@/components/NotesSheet.vue'
import DuplicateAlbums from '@/components/DuplicateAlbums.vue'
import AlbumTakeaway from '@/components/AlbumTakeaway.vue'
import {
    appUpdate,
    autoCheckUpdates,
    checkForUpdates,
    hasAppService,
    loadAppInfo,
    loadChangelog,
    openExternal,
    releasePageUrl,
} from '@/utils/appUpdate'
import { minuteClock } from '@/utils/accountStorage'
import { formatTimeAgo } from '@/utils/format'
import { albumStore } from '@/utils/albums'

const changelogOpen = ref(false)
const changelog = ref('')
const changelogLoading = ref(false)

async function openChangelog() {
    changelogOpen.value = true
    if (changelog.value) return
    changelogLoading.value = true
    changelog.value = await loadChangelog()
    changelogLoading.value = false
}

/** "2026-11-15T10:04:31Z" becomes a local date; empty for local builds. */
const buildDate = computed(() => {
    const date = appUpdate.info?.BuildDate ? new Date(appUpdate.info.BuildDate) : null
    return date && !Number.isNaN(date.getTime()) ? date.toLocaleDateString() : ''
})

const updateStatus = computed(() => {
    if (appUpdate.checking) return 'Checking GitHub…'
    if (appUpdate.error) return `Last check failed: ${appUpdate.error}`
    const update = appUpdate.update
    const checked = appUpdate.lastCheckedAt ? ` · checked ${formatTimeAgo(appUpdate.lastCheckedAt, minuteClock.value)}` : ''
    if (!update) return 'Not checked yet.'
    if (update.Newer) return `Version ${update.Latest} is available${checked}`
    if (!update.Latest) return `No releases published yet${checked}`
    return `Up to date${checked}`
})

const speedLimitRows = [
    {
        id: 'upload-limit',
        key: 'uploadLimitMB',
        configKey: 'uploadLimitKBps',
        title: 'Upload speed limit (MB/s)',
        description: 'Total for all upload threads. Changes apply right away, also to a running upload.',
    },
    {
        id: 'download-limit',
        key: 'downloadLimitMB',
        configKey: 'downloadLimitKBps',
        title: 'Download speed limit (MB/s)',
        description: 'Used by the Live Photo repair.',
    },
] as const

const closeOptions: { value: CloseBehavior, label: string }[] = [
  { value: 'ask', label: 'Ask' },
  { value: 'tray', label: 'Minimize to tray' },
  { value: 'quit', label: 'Quit' },
]
import { FolderPlus, FolderX } from '@lucide/vue'

import {
    NumberField,
    NumberFieldContent,
    NumberFieldDecrement,
    NumberFieldIncrement,
    NumberFieldInput,
} from '@/components/ui/number-field'

interface Settings {
    proxy: string
    useQuota: boolean
    saver: boolean
    recursive: boolean
    forceUpload: boolean
    pairLivePhotos: boolean
    skipIncompleteLivePhotos: boolean
    updateExistingPhotosToLive: boolean
    deleteFromHost: boolean
    disableUnsupportedFilesFilter: boolean
    setDateFromFilename: boolean
    uploadThreads: number
    watchEnabled: boolean
    watchFolders: string[]
    /** Watch folder → album key; folders without one upload without an album. */
    watchAlbums: Record<string, string>
    startWithWindows: boolean
    /** Speed limits in MB/s for the inputs; 0 = no limit. Saved as KB/s. */
    uploadLimitMB: number
    downloadLimitMB: number
}

/** The speed-limit part of the config; older bindings do not declare it yet. */
interface SpeedLimitConfig {
    uploadLimitKBps?: number
    downloadLimitKBps?: number
}
const setSpeedLimits = (ConfigManager as unknown as {
    SetSpeedLimits?: (uploadKBps: number, downloadKBps: number) => Promise<void>
}).SetSpeedLimits

const toMB = (kbps: number | undefined) => Math.round(((kbps ?? 0) / 1024) * 10) / 10
const toKBps = (mb: number) => Math.max(0, Math.round((Number.isFinite(mb) ? mb : 0) * 1024))

const settings = ref<Settings>({
    proxy: '',
    useQuota: false,
    saver: false,
    recursive: false,
    forceUpload: false,
    pairLivePhotos: false,
    skipIncompleteLivePhotos: true,
    updateExistingPhotosToLive: false,
    deleteFromHost: false,
    disableUnsupportedFilesFilter: false,
    setDateFromFilename: false,
    uploadThreads: 0,
    watchEnabled: false,
    watchFolders: [],
    watchAlbums: {},
    startWithWindows: false,
    uploadLimitMB: 0,
    downloadLimitMB: 0,
})
const isHydrating = ref(true)
const isPickingFolder = ref(false)

function notifyWatchSettingsChanged() {
    Events.Emit('watchSettingsChanged')
}

async function addWatchFolder() {
    isPickingFolder.value = true
    try {
        const folder = await DialogService.PickFolder()
        if (!folder) return
        await ConfigManager.AddWatchFolder(folder)
        if (!settings.value.watchFolders.includes(folder)) {
            settings.value.watchFolders.push(folder)
        }
        if (!settings.value.watchEnabled) {
            settings.value.watchEnabled = true
        }
        notifyWatchSettingsChanged()
    } catch (error) {
        console.error('Failed to pick watch folder:', error)
    } finally {
        isPickingFolder.value = false
    }
}

/** Reads "folder|album" entries. */
function parseWatchAlbums(entries: string[] | null | undefined): Record<string, string> {
    const albums: Record<string, string> = {}
    for (const entry of entries ?? []) {
        const at = entry.indexOf('|')
        if (at > 0 && entry.slice(at + 1).trim()) albums[entry.slice(0, at)] = entry.slice(at + 1).trim()
    }
    return albums
}

const setWatchFolderAlbum = (ConfigManager as unknown as {
    SetWatchFolderAlbum?: (folder: string, album: string) => Promise<void>
}).SetWatchFolderAlbum

/** Albums for the watch folder menus, by title. */
const albumChoices = computed(() =>
    [...albumStore.albums].sort((a, b) => a.Title.localeCompare(b.Title)),
)

function watchAlbumOf(folder: string): string {
    const match = Object.keys(settings.value.watchAlbums).find(key => key.toLowerCase() === folder.toLowerCase())
    return match ? settings.value.watchAlbums[match] ?? '' : ''
}

async function changeWatchAlbum(folder: string, album: string) {
    if (!setWatchFolderAlbum) return
    try {
        await setWatchFolderAlbum(folder, album)
        const next = { ...settings.value.watchAlbums }
        for (const key of Object.keys(next)) {
            if (key.toLowerCase() === folder.toLowerCase()) delete next[key]
        }
        if (album) next[folder] = album
        settings.value.watchAlbums = next
    } catch (error) {
        console.error('Failed to set the watch folder album:', error)
    }
}

async function removeWatchFolder(folder: string) {
    await ConfigManager.RemoveWatchFolder(folder)
    settings.value.watchFolders = settings.value.watchFolders.filter(f => f !== folder)
    notifyWatchSettingsChanged()
}

onMounted(async () => {
    void loadAppInfo()
    try {
        const config = await ConfigManager.GetSettings()
        settings.value = {
            proxy: config.proxy || '',
            useQuota: config.useQuota || false,
            saver: config.saver || false,
            recursive: config.recursive || false,
            forceUpload: config.forceUpload || false,
            pairLivePhotos: config.pairLivePhotos || false,
            skipIncompleteLivePhotos: config.skipIncompleteLivePhotos ?? true,
            updateExistingPhotosToLive: config.updateExistingPhotosToLive || false,
            deleteFromHost: config.deleteFromHost || false,
            disableUnsupportedFilesFilter: config.disableUnsupportedFilesFilter || false,
            setDateFromFilename: config.setDateFromFilename || false,
            uploadThreads: config.uploadThreads || 1,
            watchEnabled: config.watchEnabled || false,
            watchFolders: config.watchFolders || [],
            watchAlbums: parseWatchAlbums((config as { watchAlbums?: string[] | null }).watchAlbums),
            startWithWindows: config.startWithWindows || false,
            uploadLimitMB: toMB((config as SpeedLimitConfig).uploadLimitKBps),
            downloadLimitMB: toMB((config as SpeedLimitConfig).downloadLimitKBps),
        }
    } finally {
        await nextTick()
        isHydrating.value = false
    }
})

// Watch for changes to proxy value and update backend
watch(() => settings.value.proxy, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetProxy(newValue)
})

// Create individual watchers for each boolean setting
watch(() => settings.value.useQuota, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetUseQuota(newValue)
})

watch(() => settings.value.saver, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetSaver(newValue)
})

watch(() => settings.value.recursive, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetRecursive(newValue)
})

watch(() => settings.value.forceUpload, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetForceUpload(newValue)
})

watch(() => settings.value.pairLivePhotos, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetPairLivePhotos(newValue)
    if (newValue && !settings.value.skipIncompleteLivePhotos) {
        settings.value.skipIncompleteLivePhotos = true
        await ConfigManager.SetSkipIncompleteLivePhotos(true)
    }
})

watch(() => settings.value.skipIncompleteLivePhotos, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetSkipIncompleteLivePhotos(newValue)
})

watch(() => settings.value.updateExistingPhotosToLive, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetUpdateExistingPhotosToLive(newValue)
})

watch(() => settings.value.deleteFromHost, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetDeleteFromHost(newValue)
})

watch(() => settings.value.disableUnsupportedFilesFilter, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetDisableUnsupportedFilesFilter(newValue)
})

watch(() => settings.value.setDateFromFilename, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetSetDateFromFilename(newValue)
})

watch(() => settings.value.uploadThreads, async (newValue) => {
    if (isHydrating.value) return
    if (newValue < 1) {
        settings.value.uploadThreads = 1
    } else {
        await ConfigManager.SetUploadThreads(newValue)
    }
})

watch(() => settings.value.watchEnabled, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetWatchEnabled(newValue)
    notifyWatchSettingsChanged()
})

// Limits apply at once, also to an upload or repair already running.
watch(() => [settings.value.uploadLimitMB, settings.value.downloadLimitMB], async ([upload, download]) => {
    if (isHydrating.value || !setSpeedLimits) return
    await setSpeedLimits(toKBps(upload ?? 0), toKBps(download ?? 0))
})

watch(() => settings.value.startWithWindows, async (newValue) => {
    if (isHydrating.value) return
    await ConfigManager.SetStartWithWindows(newValue)
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Upload
      </h3>
      <div class="divide-y overflow-hidden rounded-xl border bg-card">
        <SettingRow
          control-id="upload-threads"
          title="Upload threads"
          description="Number of parallel upload threads."
          config-key="uploadThreads"
        >
          <NumberField
            id="upload-threads"
            v-model="settings.uploadThreads"
            :min="1"
            class="w-28 shrink-0"
          >
            <NumberFieldContent>
              <NumberFieldDecrement
                class="cursor-pointer"
                :disabled="settings.uploadThreads <= 1"
              />
              <NumberFieldInput />
              <NumberFieldIncrement class="cursor-pointer" />
            </NumberFieldContent>
          </NumberField>
        </SettingRow>
        <SettingRow
          control-id="saver-mode"
          title="Storage saver quality"
          description="Upload in storage saver quality instead of original quality."
          config-key="saver"
        >
          <Switch
            id="saver-mode"
            v-model="settings.saver"
          />
        </SettingRow>
        <SettingRow
          control-id="use-quota"
          title="Use quota"
          description="Count uploads against the account's storage quota."
          config-key="useQuota"
        >
          <Switch
            id="use-quota"
            v-model="settings.useQuota"
          />
        </SettingRow>
        <SettingRow
          control-id="force-upload"
          title="Force upload"
          description="Upload even if the file already exists in the library. Applies to single files; Live Photo pairs always check both components for duplicates."
          config-key="forceUpload"
        >
          <Switch
            id="force-upload"
            v-model="settings.forceUpload"
          />
        </SettingRow>
      </div>
    </section>

    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Files
      </h3>
      <div class="divide-y overflow-hidden rounded-xl border bg-card">
        <SettingRow
          control-id="recursive"
          title="Recursive directory upload"
          description="Include subdirectories of dropped folders."
          config-key="recursive"
        >
          <Switch
            id="recursive"
            v-model="settings.recursive"
          />
        </SettingRow>
        <SettingRow
          control-id="filter-unsupported"
          title="Disable unsupported files filter"
          description="Turn off file type filtering and try every file."
          config-key="disableUnsupportedFilesFilter"
        >
          <Switch
            id="filter-unsupported"
            v-model="settings.disableUnsupportedFilesFilter"
          />
        </SettingRow>
        <SettingRow
          control-id="set-date-from-filename"
          title="Set upload date from filename"
          description="Set the media date from the filename, e.g. 20240709_182027.jpg."
          config-key="setDateFromFilename"
        >
          <Switch
            id="set-date-from-filename"
            v-model="settings.setDateFromFilename"
          />
        </SettingRow>
      </div>
    </section>

    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Live Photos
      </h3>
      <div class="divide-y overflow-hidden rounded-xl border bg-card">
        <SettingRow
          control-id="pair-live-photos"
          title="Pair Apple Live Photos"
          description="Identify Apple Live Photo image and MOV pairs via embedded metadata and upload them as one item."
          config-key="pairLivePhotos"
        >
          <Switch
            id="pair-live-photos"
            v-model="settings.pairLivePhotos"
          />
        </SettingRow>
        <SettingRow
          control-id="skip-incomplete-live-photos"
          title="Skip incomplete Live Photos"
          description="Only upload Live Photos when both photo and video are available. If either is missing, don't upload the remaining file separately."
          config-key="skipIncompleteLivePhotos"
          nested
          :disabled="!settings.pairLivePhotos"
        >
          <Switch
            id="skip-incomplete-live-photos"
            v-model="settings.skipIncompleteLivePhotos"
            :disabled="!settings.pairLivePhotos"
          />
        </SettingRow>
        <SettingRow
          control-id="update-existing-photos-to-live"
          title="Update existing photos to Live Photos"
          description="If a static photo was already uploaded, re-upload it alongside its MOV companion to upgrade it to a Live Photo. Both local photo and video files are required."
          config-key="updateExistingPhotosToLive"
          nested
          :disabled="!settings.pairLivePhotos"
        >
          <Switch
            id="update-existing-photos-to-live"
            v-model="settings.updateExistingPhotosToLive"
            :disabled="!settings.pairLivePhotos"
          />
        </SettingRow>
      </div>
    </section>

    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Network
      </h3>
      <div class="rounded-xl border bg-card p-3">
        <label
          for="proxy-url"
          class="mb-1.5 block text-sm leading-tight font-medium select-none"
        >
          Proxy
          <code
            v-if="debugEnabled"
            class="ml-1 rounded bg-muted px-1 py-px font-mono text-[10px] font-normal text-muted-foreground"
          >proxy</code>
        </label>
        <Input
          id="proxy-url"
          v-model="settings.proxy"
          type="text"
          spellcheck="false"
          autocomplete="off"
          placeholder="Proxy URL (optional)"
          class="font-mono text-xs"
        />
      </div>
      <div
        v-if="setSpeedLimits"
        class="divide-y overflow-hidden rounded-xl border bg-card"
      >
        <SettingRow
          v-for="limit in speedLimitRows"
          :key="limit.id"
          :control-id="limit.id"
          :title="limit.title"
          :description="settings[limit.key] > 0
            ? `${limit.description} Now ${settings[limit.key]} MB/s; set 0 to remove the limit.`
            : `${limit.description} Now unlimited.`"
          :config-key="limit.configKey"
        >
          <NumberField
            :id="limit.id"
            v-model="settings[limit.key]"
            :min="0"
            :step="0.5"
            :format-options="{ maximumFractionDigits: 1 }"
            class="w-32 shrink-0"
          >
            <NumberFieldContent>
              <NumberFieldDecrement
                class="cursor-pointer"
                :disabled="settings[limit.key] <= 0"
              />
              <NumberFieldInput />
              <NumberFieldIncrement class="cursor-pointer" />
            </NumberFieldContent>
          </NumberField>
        </SettingRow>
      </div>
    </section>

    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Watch folders
      </h3>
      <div class="flex flex-col divide-y overflow-hidden rounded-xl border bg-card">
        <SettingRow
          control-id="watch-enabled"
          title="Upload watch folders automatically"
          description="New photos and videos in watched folders upload on their own, like a Dropbox sync folder."
          config-key="watchEnabled"
        >
          <Switch
            id="watch-enabled"
            v-model="settings.watchEnabled"
          />
        </SettingRow>
        <div class="flex flex-col gap-2 p-3">
          <div
            v-if="settings.watchFolders.length"
            class="flex flex-col gap-1.5"
          >
            <div
              v-for="folder in settings.watchFolders"
              :key="folder"
              class="group flex items-center gap-2 rounded-lg bg-muted/50 px-2.5 py-1.5"
            >
              <span class="min-w-0 flex-1 truncate font-mono text-xs select-text">{{ folder }}</span>
              <select
                v-if="setWatchFolderAlbum"
                class="h-7 max-w-40 shrink-0 cursor-pointer truncate rounded-md border bg-background px-1.5 text-xs"
                :value="watchAlbumOf(folder)"
                :aria-label="`Album for new files in ${folder}`"
                title="Album new files from this folder go to"
                @change="changeWatchAlbum(folder, ($event.target as HTMLSelectElement).value)"
              >
                <option value="">
                  No album
                </option>
                <option
                  v-if="watchAlbumOf(folder) && !albumChoices.some(album => album.MediaKey === watchAlbumOf(folder))"
                  :value="watchAlbumOf(folder)"
                >
                  {{ watchAlbumOf(folder).startsWith('AF1Qip') ? 'Album not in the list' : watchAlbumOf(folder) }}
                </option>
                <option
                  v-for="album in albumChoices"
                  :key="album.MediaKey"
                  :value="album.MediaKey"
                >
                  {{ album.Title }}
                </option>
              </select>
              <button
                type="button"
                class="shrink-0 cursor-pointer rounded p-1 text-muted-foreground select-none hover:bg-destructive/10 hover:text-destructive"
                title="Stop watching"
                @click="removeWatchFolder(folder)"
              >
                <FolderX class="size-3.5" />
              </button>
            </div>
          </div>
          <p
            v-else
            class="text-xs text-muted-foreground"
          >
            No folders watched yet.
          </p>
          <Button
            variant="outline"
            size="sm"
            class="w-full cursor-pointer select-none"
            :disabled="isPickingFolder"
            @click="addWatchFolder"
          >
            <FolderPlus class="size-3.5" />
            {{ isPickingFolder ? 'Picking…' : 'Add folder' }}
          </Button>
          <p
            v-if="setWatchFolderAlbum && settings.watchFolders.length"
            class="text-[11px] leading-snug text-muted-foreground"
          >
            Each folder can add its new files to one of your albums. The album you pick for a dropped upload is never used here.
          </p>
        </div>
      </div>
    </section>

    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Albums
      </h3>
      <div class="divide-y overflow-hidden rounded-xl border bg-card">
        <DuplicateAlbums />
        <AlbumTakeaway />
      </div>
    </section>

    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Startup
      </h3>
      <div class="divide-y overflow-hidden rounded-xl border bg-card">
        <SettingRow
          control-id="start-with-windows"
          title="Start with Windows"
          description="Launch gotohp minimized to the tray when you log in. Watch folders and resume prompts keep working in the background."
          config-key="startWithWindows"
        >
          <Switch
            id="start-with-windows"
            v-model="settings.startWithWindows"
          />
        </SettingRow>
      </div>
    </section>

    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Danger zone
      </h3>
      <div class="overflow-hidden rounded-xl border border-destructive/40 bg-destructive/5">
        <SettingRow
          control-id="delete-host"
          title="Delete from host after upload"
          description="Deletes local files once they are uploaded."
          config-key="deleteFromHost"
          danger
        >
          <Switch
            id="delete-host"
            v-model="settings.deleteFromHost"
            variant="destructive"
          />
        </SettingRow>
      </div>
    </section>

    <section class="flex flex-col gap-1.5">
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        Advanced
      </h3>
      <div class="divide-y overflow-hidden rounded-xl border bg-card">
        <SettingRow
          control-id="show-thumbnails"
          title="Show thumbnails"
          description="Preview photos and videos while they upload. Shown only when the window is wide enough; turn off to save memory and disk reads."
        >
          <Switch
            id="show-thumbnails"
            v-model="thumbnailsEnabled"
          />
        </SettingRow>
        <div class="flex flex-col gap-2 px-3 py-2.5">
          <span class="flex flex-col gap-0.5 select-none">
            <span
              id="close-behavior-label"
              class="text-sm leading-tight font-medium"
            >When closing the window</span>
            <span class="text-xs leading-snug text-muted-foreground">
              In the tray, uploads and watch folders keep running. Use the tray icon to open or quit gotohp.
            </span>
          </span>
          <div
            class="grid grid-cols-3 gap-1 rounded-lg bg-muted/60 p-1"
            role="radiogroup"
            aria-labelledby="close-behavior-label"
          >
            <button
              v-for="option in closeOptions"
              :key="option.value"
              type="button"
              role="radio"
              :aria-checked="closeBehavior === option.value"
              class="cursor-pointer rounded-md px-2 py-1.5 text-xs font-medium transition-colors select-none"
              :class="closeBehavior === option.value
                ? 'bg-background text-foreground shadow-xs'
                : 'text-muted-foreground hover:text-foreground'"
              @click="closeBehavior = option.value"
            >
              {{ option.label }}
            </button>
          </div>
        </div>
        <SettingRow
          control-id="debug-mode"
          title="Debug mode"
          description="Show technical details and the debug panel: live event log, state, config and system info. Shortcut: Ctrl/Cmd + Shift + D."
        >
          <Switch
            id="debug-mode"
            v-model="debugEnabled"
          />
        </SettingRow>
      </div>
    </section>

    <section
      v-if="hasAppService()"
      class="flex flex-col gap-1.5"
    >
      <h3 class="px-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase select-none">
        About
      </h3>
      <div class="divide-y overflow-hidden rounded-xl border bg-card">
        <div class="flex items-center gap-3 px-3 py-2.5">
          <span class="flex min-w-0 flex-1 flex-col gap-0.5 select-text">
            <span class="text-sm leading-tight font-medium">
              gotohp {{ appUpdate.info?.Version || '…' }}
            </span>
            <span class="text-xs leading-snug text-muted-foreground">
              <template v-if="appUpdate.info?.Commit">
                Built {{ buildDate || appUpdate.info.BuildDate }} from
                <code class="font-mono">{{ appUpdate.info.Commit }}</code>
              </template>
              <template v-else>
                Local build
              </template>
            </span>
          </span>
          <Button
            variant="outline"
            size="sm"
            class="shrink-0 cursor-pointer"
            @click="openChangelog"
          >
            What's new
          </Button>
        </div>
        <div class="flex items-center gap-3 px-3 py-2.5">
          <span class="flex min-w-0 flex-1 flex-col gap-0.5">
            <span class="text-sm leading-tight font-medium">Updates</span>
            <span
              class="text-xs leading-snug"
              :class="appUpdate.update?.Newer && !appUpdate.checking ? 'text-primary' : 'text-muted-foreground'"
            >{{ updateStatus }}</span>
          </span>
          <Button
            v-if="appUpdate.update?.Newer && !appUpdate.checking"
            size="sm"
            class="shrink-0 cursor-pointer"
            @click="openExternal(releasePageUrl())"
          >
            Download
          </Button>
          <Button
            variant="outline"
            size="sm"
            class="shrink-0 cursor-pointer"
            :disabled="appUpdate.checking"
            @click="checkForUpdates(true)"
          >
            {{ appUpdate.checking ? 'Checking…' : 'Check now' }}
          </Button>
        </div>
        <SettingRow
          control-id="auto-update-check"
          title="Check for updates automatically"
          description="Once a day when gotohp starts, asks GitHub whether a newer release exists. Nothing is downloaded or installed without you."
        >
          <Switch
            id="auto-update-check"
            v-model="autoCheckUpdates"
          />
        </SettingRow>
      </div>
    </section>

    <NotesSheet
      v-model:open="changelogOpen"
      title="What's new"
      :description="`Changes in this fork of gotohp, up to version ${appUpdate.info?.Version ?? ''}.`"
      :source="changelog"
      :loading="changelogLoading"
      empty-text="No changelog is bundled with this build."
    />
  </div>
</template>
