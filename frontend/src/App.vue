<script setup lang="ts">
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { useColorMode } from '@vueuse/core'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Activity,
  Aperture,
  AlertTriangle,
  Bug,
  Check,
  ChevronDown,
  CircleCheck,
  CircleX,
  Copy,
  ExternalLink,
  FolderPlus,
  FolderTree,
  HardDrive,
  KeyRound,
  PanelBottomClose,
  Pause,
  Play,
  Power,
  RotateCcw,
  Settings,
  SkipForward,
  Trash2,
  UploadCloud,
  UserPlus,
} from '@lucide/vue'
import { ConfigManager } from '../bindings/app/core'
import { Events, System } from '@wailsio/runtime'
import Button from "./components/ui/button/Button.vue"
import GoogleAccountSelect from './components/GoogleAccountSelect.vue'
import GoogleAuthSetup from "./components/GoogleAuthSetup.vue"
import './index.css'
import SettingsPanel from "./SettingsPanel.vue"
import StatsPanel from "./StatsPanel.vue"
import StoragePage from './pages/StoragePage.vue'
import BinPage from './pages/BinPage.vue'
import ActivityPage from './pages/ActivityPage.vue'
import * as guiBindings from '../bindings/app/internal/gui'
import { quotaConvert } from './utils/quotaConvert'
import Upload from './Upload.vue'
import { uploadManager } from './utils/UploadManager'
import Toaster from './components/ui/sonner/Sonner.vue'
import DebugPanel from './components/DebugPanel.vue'
import AlbumPicker from './components/AlbumPicker.vue'
import { albumTitle, prefetchAlbums, refreshAlbums, rememberAlbum } from './utils/albums'
import UpdateBanner from './components/UpdateBanner.vue'
import { maybeAutoCheckForUpdates } from './utils/appUpdate'
import MediaThumb from './components/MediaThumb.vue'
import { thumbnailsActive } from './utils/thumbnails'
import { resetAccountStorage } from './utils/accountStorage'
import { liveRepair } from './utils/liveRepair'
import logoUrl from './assets/logo.svg'
import {
  debugCounts,
  debugEnabled,
  debugLog,
  installGlobalDebugHandlers,
  toggleDebug,
} from './utils/debug'
import { baseName, formatBytes, formatDuration } from './utils/format'
import { closeBehavior } from './utils/closeBehavior'
import { openInGooglePhotos } from './utils/googlePhotos'

import { toast } from "vue-sonner"

useColorMode().value = "dark"

installGlobalDebugHandlers()

const { state: uploadState } = uploadManager
const copyButtonText = ref('Copy as JSON');

// macOS draws the window buttons over the top-left corner of the content
const isMac = (() => {
  try {
    return System.IsMac()
  } catch {
    return false
  }
})()

// Sidebar pages
type Page = 'upload' | 'storage' | 'bin' | 'live' | 'activity' | 'settings'
const page = ref<Page>('upload')
const pages: { id: Page, label: string, icon: typeof Activity }[] = [
  { id: 'upload', label: 'Upload', icon: UploadCloud },
  { id: 'storage', label: 'Storage', icon: HardDrive },
  { id: 'bin', label: 'Bin', icon: Trash2 },
  { id: 'live', label: 'Live Photos', icon: Aperture },
  { id: 'activity', label: 'Activity', icon: Activity },
]
const pageTitles: Record<Page, string> = {
  upload: 'Upload',
  storage: 'Storage',
  bin: 'Bin',
  live: 'Live Photos',
  activity: 'Activity',
  settings: 'Settings',
}
const pageDescriptions: Record<Page, string> = {
  upload: '',
  storage: 'Files that count against your Google storage, and making them unlimited.',
  bin: 'Items in your Google Photos bin. Google deletes them after 60 days; until then they still count against your storage.',
  live: '',
  activity: 'What this app has uploaded, and what went wrong.',
  settings: 'Changes are saved automatically and apply to the next upload.',
}
/** Something is running behind a page, shown as a dot in the sidebar. */
function pageBusy(id: Page): boolean {
  if (id === 'upload') return uploadState.isUploading || uploadState.isStarting || Boolean(uploadState.paused)
  if (id === 'storage') return quotaConvert.running
  if (id === 'live') return liveRepair.running
  return false
}

// Reached by name so older bindings still build.
const libraryApi = (guiBindings as unknown as {
  LibraryService?: {
    AccountPhoto?: (email: string) => Promise<string>
    AddToAlbum?: (mediaKeys: string[], albumNameOrKey: string) => Promise<string[]>
  }
}).LibraryService

// The selected account's photo; its initial is shown until it loads.
const accountPhoto = ref('')
const accountPhotos: Record<string, string> = {}
async function loadAccountPhoto(email: string) {
  accountPhoto.value = accountPhotos[email] ?? ''
  if (!email || !libraryApi?.AccountPhoto || accountPhotos[email]) return
  try {
    const photo = await libraryApi.AccountPhoto(email)
    if (photo) accountPhotos[email] = photo
    if (selectedOption.value === email) accountPhoto.value = photo
  } catch {
    // The initial stays.
  }
}

// Closing the window: the backend holds the close and asks here what to do
const isCloseDialogOpen = ref(false)
const rememberCloseChoice = ref(false)
const watchFoldersActive = ref(false)
let closeAnswered = false

function answerClose(choice: 'tray' | 'quit' | 'cancel') {
  closeAnswered = true
  if (choice !== 'cancel' && rememberCloseChoice.value) {
    closeBehavior.value = choice
  }
  rememberCloseChoice.value = false
  isCloseDialogOpen.value = false
  debugLog('info', 'frontend', 'closeChoice', { choice })
  Events.Emit('closeChoice', choice)
}

async function onCloseRequested() {
  debugLog('event', 'backend', 'closeRequested')
  if (closeBehavior.value !== 'ask') {
    answerClose(closeBehavior.value)
    return
  }
  try {
    const config = await ConfigManager.GetSettings()
    watchFoldersActive.value = config.watchEnabled && (config.watchFolders?.length ?? 0) > 0
  } catch {
    watchFoldersActive.value = false
  }
  closeAnswered = false
  isCloseDialogOpen.value = true
}

// Dismissing the question (Escape, click outside) keeps the window open
watch(isCloseDialogOpen, (open) => {
  if (!open && !closeAnswered) answerClose('cancel')
})
const isDebugOpen = ref(false)
const showResultDetails = ref(false)

// Interrupted upload run offered for resume at startup
interface ResumeSummary {
  Exists: boolean
  StartedAt: number
  Total: number
  Done: number
  Skipped: number
  Failed: number
  Pending: number
  PendingBytes: number
  SourcePaths: string
}
const resumeOffer = ref<ResumeSummary | null>(null)

const resumeTitle = computed(() => {
  if (!resumeOffer.value) return ''
  const base = baseName(resumeOffer.value.SourcePaths || '')
  return base || 'Previous upload'
})

const resumeDetail = computed(() => {
  if (!resumeOffer.value) return ''
  const s = resumeOffer.value
  const parts: string[] = []
  if (s.Done > 0) parts.push(`${s.Done} uploaded`)
  if (s.Skipped > 0) parts.push(`${s.Skipped} skipped`)
  if (s.Failed > 0) parts.push(`${s.Failed} failed`)
  return `${parts.join(' · ')} — ${s.Pending} remaining (${formatBytes(s.PendingBytes)})`
})

function acceptResume() {
  resumeOffer.value = null
  Events.Emit('resumeUpload')
}

function discardResume() {
  resumeOffer.value = null
  Events.Emit('discardUpload')
}

// Active upload options, shown as chips on the home screen
interface UploadPrefs {
  saver: boolean
  recursive: boolean
  forceUpload: boolean
  pairLivePhotos: boolean
  deleteFromHost: boolean
  uploadThreads: number
  proxy: string
}
const prefs = ref<UploadPrefs | null>(null)

async function refreshPrefs() {
  try {
    const config = await ConfigManager.GetSettings()
    prefs.value = {
      saver: config.saver,
      recursive: config.recursive,
      forceUpload: config.forceUpload,
      pairLivePhotos: config.pairLivePhotos,
      deleteFromHost: config.deleteFromHost,
      uploadThreads: config.uploadThreads || 1,
      proxy: config.proxy || '',
    }
  } catch (error) {
    console.error('Failed to load settings:', error)
    debugLog('error', 'frontend', 'GetSettings failed', error instanceof Error ? error.message : String(error))
  }
}

watch(page, (current, previous) => {
  if (previous === 'settings' && current !== 'settings') refreshPrefs()
})

watch(debugEnabled, (enabled) => {
  if (!enabled) isDebugOpen.value = false
})

// Ctrl/Cmd + Shift + D toggles debug mode
const onKeyDown = (e: KeyboardEvent) => {
  if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === 'd') {
    e.preventDefault()
    toggleDebug()
    toast(debugEnabled.value ? 'Debug mode on' : 'Debug mode off', { duration: 1500 })
  }
}

// An upload is running, or a queued one is about to start
const isActive = computed(() => uploadState.isUploading || uploadState.isStarting)

// Drop zones stay available during an upload so more files can be queued
const showDropZones = computed(() => isDraggingFiles.value && options.value.length > 0)

const hasResults = computed(() => {
  const { success, fail, skipped } = uploadState.results
  return success.length + fail.length + skipped.length > 0
})

const lastBatchSummary = computed(() => {
  const parts: string[] = []
  // A run covers the first drop plus everything that was queued behind it
  const runStart = uploadState.runStartTime || uploadState.startTime
  if (runStart > 0 && uploadState.endTime > runStart) {
    parts.push(formatDuration((uploadState.endTime - runStart) / 1000))
  }
  if (uploadState.runBytes > 0) {
    parts.push(formatBytes(uploadState.runBytes))
  }
  if (uploadState.runJobs > 1) {
    parts.push(`${uploadState.runJobs} drops`)
  }
  return parts.join(' · ')
})

const MAX_RESULT_ROWS = 200

interface ResultRow {
  kind: 'success' | 'fail' | 'skipped' | 'warning'
  title: string
  detail: string
  technical: string
  /** Set for uploaded files, so a thumbnail made during the upload can be shown. */
  path?: string
  /** Set for uploaded files, to open them in Google Photos. */
  mediaKey?: string
}

const allResultRows = computed<ResultRow[]>(() => {
  const { success, fail, skipped, warnings } = uploadState.results
  return [
    ...fail.map((message): ResultRow => ({ kind: 'fail', title: message, detail: '', technical: '' })),
    ...warnings.map((item): ResultRow => ({
      kind: 'warning',
      title: item.paths.map(baseName).join(', '),
      detail: item.reason,
      technical: `code=${item.code}\n${item.paths.join('\n')}`,
    })),
    ...skipped.map((item): ResultRow => ({
      kind: 'skipped',
      title: item.paths.map(baseName).join(', '),
      detail: item.reason,
      technical: `code=${item.code}\n${item.paths.join('\n')}`,
    })),
    ...success.map((item): ResultRow => ({
      kind: 'success',
      title: baseName(item.path),
      detail: '',
      technical: `mediaKey=${item.mediaKey}\n${item.path}`,
      path: item.path,
      mediaKey: item.mediaKey,
    })),
  ]
})

const resultRows = computed(() => allResultRows.value.slice(0, MAX_RESULT_ROWS))

const totalEventCount = computed(() =>
  Object.values(debugCounts.value).reduce((sum, count) => sum + count, 0),
)

const viewport = ref(`${window.innerWidth}×${window.innerHeight}`)
const windowWidth = ref(window.innerWidth)
// A narrow window shows the sidebar as icons only.
const narrow = computed(() => windowWidth.value < 720)
const onResize = () => {
  viewport.value = `${window.innerWidth}×${window.innerHeight}`
  windowWidth.value = window.innerWidth
}

// Drag state for dual drop zones
const isDraggingFiles = ref(false)

// Album upload flow state
const showAlbumInput = ref(false)
const pendingFiles = ref<string[]>([])
const pendingFileCount = ref(0)
// Uploaded items whose album could not be reached, waiting for another album.
const pendingMediaKeys = ref<string[]>([])

const selectedOption = ref('')
const options = ref<string[]>([])
const accountNeedsTokenBinding = ref<Record<string, boolean>>({})
const albumNameOrKey = ref('')
const tokenBindingEmail = ref('')
const isExtractingTokenBinding = ref(false)
const isAccountSetupOpen = ref(false)
const removingAccount = ref('')

// Storage numbers belong to one account; forget them when it changes
watch(selectedOption, (account) => resetAccountStorage(account), { immediate: true })

watch(selectedOption, (email) => void loadAccountPhoto(email), { immediate: true })

watch(selectedOption, async (newValue, oldValue) => {
  if (newValue) {
    try {
      await ConfigManager.SetSelected(newValue)
      await updateTokenBindingPrompt(newValue)
      // Another account has other albums.
      if (oldValue && oldValue !== newValue) void refreshAlbums(true)
      console.log('Successfully updated selected value:', newValue)
    } catch (error) {
      console.error('Failed to update selected value:', error)
      toast.error('Failed to update selected account.')
    }
  } else {
    tokenBindingEmail.value = ''
  }
})

async function updateTokenBindingPrompt(email: string) {
  tokenBindingEmail.value = accountNeedsTokenBinding.value[email] ? email : ''
}

async function refreshCredentials() {
  try {
    const state = await ConfigManager.GetAccounts()
    const nextNeedsTokenBinding: Record<string, boolean> = {}
    const nextOptions = state.accounts.map(account => {
      nextNeedsTokenBinding[account.email] = account.needsTokenBinding
      return account.email
    })

    accountNeedsTokenBinding.value = nextNeedsTokenBinding
    options.value = nextOptions
    selectedOption.value = state.selected || ''
    if (state.selected) {
      await updateTokenBindingPrompt(state.selected)
    } else {
      tokenBindingEmail.value = ''
    }
  } catch (error) {
    console.error('Failed to refresh Google accounts:', error)
    toast.error('Could not refresh Google accounts', {
      description: error instanceof Error ? error.message : String(error),
    })
  }
}

async function addTokenBindingAliasFromADB() {
  if (!tokenBindingEmail.value) return

  isExtractingTokenBinding.value = true
  try {
    await ConfigManager.AddTokenBindingAliasFromADB(tokenBindingEmail.value)
    await refreshCredentials()
    tokenBindingEmail.value = ''
    toast.success('Token binding key added.')
  } catch (error) {
    console.error('Failed to add token binding key:', error)
    toast.error('Failed to add token binding key', {
      description: error instanceof Error ? error.message : String(error),
    })
  } finally {
    isExtractingTokenBinding.value = false
  }
}

async function removeCredentials(email: string) {
  removingAccount.value = email
  try {
    debugLog('info', 'frontend', 'RemoveCredentials', { email })
    await ConfigManager.RemoveCredentials(email)

    const removedSelectedAccount = selectedOption.value === email
    await refreshCredentials()
    if (removedSelectedAccount && options.value.length > 0 && !selectedOption.value) {
      selectedOption.value = options.value[0]
    }
    toast.success('Credentials removed.')
    return true
  } catch (error) {
    console.error('Failed to remove credentials:', error)
    toast.error('Failed to remove credentials.')
    return false
  } finally {
    removingAccount.value = ''
  }
}

function openAccountSetup() {
  isAccountSetupOpen.value = true
}

onMounted(async () => {
  await Promise.all([refreshCredentials(), refreshPrefs()])
  // Albums load in the background so the album picker is ready when needed.
  if (options.value.length > 0) void prefetchAlbums()
  // At most once a day, and quietly unless a newer release exists.
  void maybeAutoCheckForUpdates()
})

const handleCopyClick = () => {
  uploadManager.copyResultsAsJson();
  copyButtonText.value = 'Copied!';
  setTimeout(() => copyButtonText.value = 'Copy as JSON', 1000);
};

// Global drag event handlers to detect file dragging
let dragLeaveTimeout: ReturnType<typeof setTimeout> | null = null

const onDragEnter = (e: DragEvent) => {
  if (!e.dataTransfer?.types.includes('Files')) return
  
  // Clear any pending drag leave timeout
  if (dragLeaveTimeout) {
    clearTimeout(dragLeaveTimeout)
    dragLeaveTimeout = null
  }
  
  isDraggingFiles.value = true
}

const onDragOver = (e: DragEvent) => {
  if (!e.dataTransfer?.types.includes('Files')) return
  e.preventDefault()
  
  // Clear any pending drag leave timeout - we're still dragging
  if (dragLeaveTimeout) {
    clearTimeout(dragLeaveTimeout)
    dragLeaveTimeout = null
  }
}

const onDragLeave = (e: DragEvent) => {
  if (!e.dataTransfer?.types.includes('Files')) return
  
  // Use timeout to detect if we've truly left the window
  // dragover will cancel this if we're still in the window
  if (dragLeaveTimeout) {
    clearTimeout(dragLeaveTimeout)
  }
  dragLeaveTimeout = setTimeout(() => {
    isDraggingFiles.value = false
    dragLeaveTimeout = null
  }, 50)
}

const onDrop = () => {
  // Clear any pending timeout
  if (dragLeaveTimeout) {
    clearTimeout(dragLeaveTimeout)
    dragLeaveTimeout = null
  }
  
  // Delay resetting isDraggingFiles to allow Wails to process the drop target
  // before Vue re-renders and hides the drop zones
  setTimeout(() => {
    isDraggingFiles.value = false
  }, 100)
}

// Starts the upload, or queues it when one is already running
async function submitUpload(files: string[], albumName: string, albumAutoMode: boolean) {
  try {
    const outcome = await uploadManager.enqueue({ files, albumName, albumAutoMode })
    if (outcome === 'queued') {
      toast.success('Added to queue', {
        description: `${files.length} dropped item(s) will upload when the current upload finishes.`,
        duration: 2500,
      })
    }
  } catch (error) {
    console.error('Failed to start upload:', error)
    debugLog('error', 'frontend', 'startUpload failed', error instanceof Error ? error.message : String(error))
    toast.error('Failed to start upload', {
      description: error instanceof Error ? error.message : String(error),
    })
  }
}

// Album upload confirmation
const confirmAlbumUpload = async () => {
  const files = pendingFiles.value
  const mediaKeys = pendingMediaKeys.value
  const albumName = albumNameOrKey.value.trim()
  showAlbumInput.value = false
  pendingFiles.value = []
  pendingMediaKeys.value = []
  pendingFileCount.value = 0
  albumNameOrKey.value = '' // Reset for next upload
  if (!albumName) return
  rememberAlbum(albumName)
  if (mediaKeys.length > 0) {
    await addUploadedToAlbum(mediaKeys, albumName)
    return
  }
  await submitUpload(files, albumName, false)
}

const cancelAlbumUpload = () => {
  showAlbumInput.value = false
  pendingFiles.value = []
  pendingMediaKeys.value = []
  pendingFileCount.value = 0
  albumNameOrKey.value = '' // Reset on cancel too
}

/** Puts already uploaded items into an album, after their own album failed. */
async function addUploadedToAlbum(mediaKeys: string[], albumNameOrKey: string) {
  if (!libraryApi?.AddToAlbum) return
  const name = albumTitle(albumNameOrKey) || albumNameOrKey
  try {
    await libraryApi.AddToAlbum(mediaKeys, albumNameOrKey)
    toast.success(`Added ${mediaKeys.length} file(s) to ${name}`)
    void refreshAlbums(true)
  } catch (error) {
    toast.error(`Could not add to ${name}`, { description: error instanceof Error ? error.message : String(error) })
  }
}

/** Opens the album picker for items that are already uploaded. */
function pickAlbumFor(mediaKeys: string[], suggestedName: string) {
  pendingMediaKeys.value = mediaKeys
  pendingFiles.value = []
  pendingFileCount.value = mediaKeys.length
  albumNameOrKey.value = suggestedName
  showAlbumInput.value = true
  page.value = 'upload'
}

// Handle album error event
const albumErrorHandler = (e: Event) => {
  const event = e as CustomEvent<{ AlbumName: string; Error: string; NotFound?: boolean; MediaKeys?: string[] | null }>
  const { AlbumName, Error, MediaKeys } = event.detail
  const notFound = event.detail.NotFound ?? (AlbumName.startsWith('AF1Qip') && Error.includes('404'))
  const keys = MediaKeys ?? []
  const title = albumTitle(AlbumName) || (AlbumName.startsWith('AF1Qip') ? '' : AlbumName)
  const count = keys.length ? `${keys.length} file(s) uploaded` : 'The files uploaded'
  // The album may have been deleted or renamed: read the list again.
  void refreshAlbums(true)
  toast.warning(notFound ? `Album ${title ? `“${title}” ` : ''}wasn't found` : `Could not add to ${title ? `“${title}”` : 'the album'}`, {
    id: `album-error-${AlbumName}`,
    description: `${count} without an album.${notFound ? '' : ` ${Error}`}`,
    duration: 20000,
    action: keys.length && libraryApi?.AddToAlbum
      ? { label: 'Pick an album', onClick: () => pickAlbumFor(keys, title) }
      : undefined,
    cancel: { label: 'Leave out', onClick: () => {} },
  })
}

// Failures share one toast that counts up, instead of one toast per file.
let failedSinceToast = 0
let failureToastTimer: ReturnType<typeof setTimeout> | null = null
const uploadErrorHandler = (e: Event) => {
  const event = e as CustomEvent<{ FileName: string; Message: string }>
  const { FileName, Message } = event.detail
  const errorMessage = Message.replace(/^Error:\s*/, '')
  failedSinceToast++
  if (failureToastTimer) clearTimeout(failureToastTimer)
  failureToastTimer = setTimeout(() => { failedSinceToast = 0 }, 15000)
  const several = failedSinceToast > 1
  toast.error(several ? `${failedSinceToast} files failed to upload` : FileName ? `Upload failed: ${FileName}` : 'Upload failed', {
    id: 'upload-failures',
    description: several ? `Latest: ${FileName ? `${FileName}: ` : ''}${errorMessage}` : errorMessage,
    duration: 10000,
    action: { label: 'Details', onClick: () => { page.value = 'activity' } },
  })
}

onMounted(() => {
  document.addEventListener('dragenter', onDragEnter)
  document.addEventListener('dragleave', onDragLeave)
  document.addEventListener('dragover', onDragOver)
  document.addEventListener('drop', onDrop)
  window.addEventListener('albumError', albumErrorHandler)
  window.addEventListener('uploadError', uploadErrorHandler)
  window.addEventListener('keydown', onKeyDown)
  window.addEventListener('resize', onResize)

  Events.On('closeRequested', onCloseRequested)

  // Listen for files-dropped event from backend
  Events.On('files-dropped', async (event: { data: { files: string[]; dropZone: string } }) => {
    const { files, dropZone } = event.data
    debugLog('event', 'backend', 'files-dropped', event.data)
    page.value = 'upload'

    if (dropZone === 'album') {
      // Show album input screen
      pendingMediaKeys.value = []
      pendingFiles.value = files
      pendingFileCount.value = files.length
      showAlbumInput.value = true
    } else {
      await submitUpload(files, '', dropZone === 'auto-album')
    }
  })

  // Interrupted upload offered for resume by the backend at startup
  Events.On('resumePending', (event: { data: ResumeSummary }) => {
    debugLog('event', 'backend', 'resumePending', event.data)
    if (event.data?.Exists && event.data.Pending > 0) {
      resumeOffer.value = event.data
    }
  })
})

onUnmounted(() => {
  document.removeEventListener('dragenter', onDragEnter)
  document.removeEventListener('dragleave', onDragLeave)
  document.removeEventListener('dragover', onDragOver)
  document.removeEventListener('drop', onDrop)
  window.removeEventListener('albumError', albumErrorHandler)
  window.removeEventListener('uploadError', uploadErrorHandler)
  window.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('resize', onResize)
  if (dragLeaveTimeout) {
    clearTimeout(dragLeaveTimeout)
  }
})
</script>

<template>
  <main
    class="flex h-screen w-screen overflow-hidden"
    style="--wails-draggable: drag"
  >
    <!-- Sidebar -->
    <aside
      v-if="options.length > 0"
      class="flex shrink-0 flex-col gap-0.5 border-r bg-background/60 pb-2 transition-[width] duration-200"
      :class="[narrow ? 'w-14 px-1.5' : 'w-52 px-2', isMac ? 'pt-10' : 'pt-2.5']"
    >
      <div
        class="mb-2 flex items-center gap-2 px-1.5 select-none"
        :class="narrow && 'justify-center px-0'"
      >
        <img
          :src="logoUrl"
          alt=""
          class="size-6 shrink-0"
        >
        <span
          v-if="!narrow"
          class="text-sm font-semibold"
        >gotohp</span>
      </div>
      <nav
        class="flex flex-col gap-0.5"
        style="--wails-draggable: none"
        aria-label="Pages"
      >
        <button
          v-for="item in pages"
          :key="item.id"
          type="button"
          class="relative flex cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm select-none"
          :class="[
            page === item.id ? 'bg-primary/12 text-foreground' : 'text-muted-foreground hover:bg-accent hover:text-foreground',
            narrow && 'justify-center px-0',
          ]"
          :title="narrow ? item.label : undefined"
          :aria-current="page === item.id ? 'page' : undefined"
          @click="page = item.id"
        >
          <component
            :is="item.icon"
            class="size-4.5 shrink-0"
            :class="page === item.id && 'text-primary'"
          />
          <span v-if="!narrow">{{ item.label }}</span>
          <span
            v-if="pageBusy(item.id)"
            class="size-1.5 rounded-full bg-primary"
            :class="narrow ? 'absolute top-1.5 right-2' : 'ml-auto animate-pulse'"
          />
        </button>
      </nav>
      <div class="flex-1" />
      <nav
        class="flex flex-col gap-0.5"
        style="--wails-draggable: none"
      >
        <button
          type="button"
          class="flex cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm select-none"
          :class="[
            page === 'settings' ? 'bg-primary/12 text-foreground' : 'text-muted-foreground hover:bg-accent hover:text-foreground',
            narrow && 'justify-center px-0',
          ]"
          :title="narrow ? 'Settings' : undefined"
          :aria-current="page === 'settings' ? 'page' : undefined"
          @click="page = 'settings'"
        >
          <Settings
            class="size-4.5 shrink-0"
            :class="page === 'settings' && 'text-primary'"
          />
          <span v-if="!narrow">Settings</span>
        </button>
        <GoogleAccountSelect
          v-if="!isActive"
          v-model="selectedOption"
          :options="options"
          :removing-account="removingAccount"
          :photo="accountPhoto"
          :compact="narrow"
          @item-removed="removeCredentials"
          @add="openAccountSetup"
        />
        <div
          v-else
          class="flex items-center gap-2 px-1 py-1 select-none"
          :class="narrow && 'justify-center'"
          :title="`${selectedOption} · the account can be changed after the upload`"
        >
          <img
            v-if="accountPhoto"
            :src="accountPhoto"
            alt=""
            class="size-6 shrink-0 rounded-full object-cover"
          >
          <span
            v-else
            class="flex size-6 shrink-0 items-center justify-center rounded-full bg-primary/15 text-[11px] font-semibold text-primary uppercase"
          >{{ selectedOption ? selectedOption[0] : '?' }}</span>
          <span
            v-if="!narrow"
            class="min-w-0 truncate text-xs text-muted-foreground"
          >{{ selectedOption }}</span>
        </div>
      </nav>
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <!-- Title bar -->
      <header
        class="flex h-11 shrink-0 items-center gap-2 px-4"
        :class="isMac && options.length === 0 && 'pl-20'"
      >
        <template v-if="options.length === 0">
          <img
            :src="logoUrl"
            alt=""
            class="size-5 shrink-0"
          >
          <span class="text-sm font-semibold select-none">gotohp</span>
        </template>
        <h1
          v-else
          class="text-base font-semibold select-none"
        >
          {{ showDropZones || showAlbumInput ? 'Upload' : pageTitles[page] }}
        </h1>
        <div
          class="ml-auto flex shrink-0 items-center gap-1"
          style="--wails-draggable: none"
        >
          <button
            v-if="debugEnabled"
            type="button"
            class="flex h-7 cursor-pointer items-center gap-1 rounded-md border border-amber-500/30 bg-amber-500/10 px-2 font-mono text-[10px] font-semibold tracking-wider text-amber-300 select-none hover:bg-amber-500/20"
            title="Open debug panel"
            @click="isDebugOpen = true"
          >
            <Bug class="size-3.5" />
            DEBUG
          </button>
          <Button
            v-if="options.length === 0"
            variant="ghost"
            size="icon"
            class="size-8 cursor-pointer"
            aria-label="Settings"
            title="Settings"
            @click="page = page === 'settings' ? 'upload' : 'settings'"
          >
            <Settings class="size-4" />
          </Button>
        </div>
      </header>

      <!-- A newer release on GitHub -->
      <UpdateBanner />

      <!-- Drop zones shown when dragging files -->
      <div
        v-if="showDropZones"
        class="flex min-h-0 flex-1 flex-col gap-2.5 p-4 md:gap-4 md:p-6"
        style="--wails-draggable: none"
      >
        <p
          v-if="isActive"
          class="text-center text-xs font-medium text-muted-foreground select-none"
        >
          Upload in progress. Drop to add to the queue.
        </p>
        <div
          data-file-drop-target
          data-drop-zone="regular"
          class="drop-zone flex flex-1 items-center gap-4 rounded-2xl md:justify-center md:gap-5 border-2 border-dashed border-muted-foreground/30 bg-card/40 px-5 transition-all duration-200"
        >
          <div class="drop-zone-icon flex size-11 shrink-0 items-center justify-center rounded-xl bg-muted text-muted-foreground transition-colors">
            <UploadCloud class="size-5" />
          </div>
          <div class="min-w-0">
            <h2 class="text-base font-semibold text-muted-foreground select-none">
              Upload only
            </h2>
            <p class="mt-0.5 text-sm text-muted-foreground/70 select-none">
              Upload files without adding to any album
            </p>
          </div>
        </div>
        <div
          data-file-drop-target
          data-drop-zone="album"
          class="drop-zone flex flex-1 items-center gap-4 rounded-2xl md:justify-center md:gap-5 border-2 border-dashed border-muted-foreground/30 bg-card/40 px-5 transition-all duration-200"
        >
          <div class="drop-zone-icon flex size-11 shrink-0 items-center justify-center rounded-xl bg-muted text-muted-foreground transition-colors">
            <FolderPlus class="size-5" />
          </div>
          <div class="min-w-0">
            <h2 class="text-base font-semibold text-muted-foreground select-none">
              Upload to album
            </h2>
            <p class="mt-0.5 text-sm text-muted-foreground/70 select-none">
              Upload and add to a specific album (you'll enter the name)
            </p>
          </div>
        </div>
        <div
          data-file-drop-target
          data-drop-zone="auto-album"
          class="drop-zone flex flex-1 items-center gap-4 rounded-2xl md:justify-center md:gap-5 border-2 border-dashed border-muted-foreground/30 bg-card/40 px-5 transition-all duration-200"
        >
          <div class="drop-zone-icon flex size-11 shrink-0 items-center justify-center rounded-xl bg-muted text-muted-foreground transition-colors">
            <FolderTree class="size-5" />
          </div>
          <div class="min-w-0">
            <h2 class="text-base font-semibold text-muted-foreground select-none">
              Auto album
            </h2>
            <p class="mt-0.5 text-sm text-muted-foreground/70 select-none">
              Upload and create albums automatically based on folder names
            </p>
          </div>
        </div>
      </div>

      <!-- Album name prompt, shown after a drop on the album zone -->
      <div
        v-else-if="showAlbumInput && options.length > 0"
        class="flex min-h-0 flex-1 flex-col items-center justify-center p-4"
        style="--wails-draggable: none"
      >
        <div class="flex w-full max-w-md flex-col gap-5 rounded-2xl border bg-card p-5">
          <div class="flex items-center gap-3">
            <div class="flex size-10 shrink-0 items-center justify-center rounded-xl bg-primary/15 text-primary">
              <FolderPlus class="size-5" />
            </div>
            <div>
              <h1 class="text-base leading-tight font-semibold select-none">
                Upload to album
              </h1>
              <p class="text-sm text-muted-foreground select-none">
                <template v-if="pendingMediaKeys.length">
                  {{ pendingFileCount }} uploaded file(s) without an album
                </template>
                <template v-else>
                  {{ pendingFileCount }} file(s) ready to {{ isActive ? 'queue' : 'upload' }}
                </template>
              </p>
            </div>
          </div>

          <AlbumPicker
            v-model="albumNameOrKey"
            @submit="albumNameOrKey.trim() && confirmAlbumUpload()"
          />

          <div class="grid grid-cols-2 gap-2">
            <Button
              variant="outline"
              class="cursor-pointer select-none"
              @click="cancelAlbumUpload"
            >
              Cancel
            </Button>
            <Button
              class="cursor-pointer select-none"
              :disabled="!albumNameOrKey.trim()"
              @click="confirmAlbumUpload"
            >
              {{ pendingMediaKeys.length ? 'Add to album' : isActive ? 'Add to queue' : 'Upload' }}
            </Button>
          </div>
        </div>
      </div>

      <!-- Normal UI (not dragging) -->
      <div
        v-else-if="!isActive && page === 'upload'"
        class="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col gap-3 overflow-y-auto p-4 md:gap-4 md:p-6"
        data-file-drop-target
      >
        <!-- No account yet -->
        <template v-if="options.length === 0">
          <div class="flex flex-1 flex-col items-center justify-center gap-5 px-4 text-center">
            <div class="flex size-16 items-center justify-center rounded-2xl bg-primary/15 ring-1 ring-primary/25">
              <img
                :src="logoUrl"
                alt=""
                class="size-9"
              >
            </div>
            <div class="flex flex-col gap-1.5">
              <h1 class="text-xl font-semibold tracking-tight select-none">
                Connect Google Photos
              </h1>
              <p class="text-sm text-muted-foreground select-none">
                Add an account before uploading photos and videos.
              </p>
            </div>
            <Button
              class="cursor-pointer select-none"
              style="--wails-draggable: none"
              @click="openAccountSetup"
            >
              <UserPlus class="size-4" />
              Add Google account
            </Button>
          </div>
        </template>

        <template v-else>
          <!-- Normal UI when not dragging -->
          <template v-if="!showAlbumInput">
            <div
              v-if="uploadState.paused"
              class="flex flex-col gap-3 rounded-xl border border-sky-500/30 bg-sky-500/10 p-3"
              style="--wails-draggable: none"
            >
              <div class="flex gap-2.5">
                <Pause class="mt-0.5 size-4 shrink-0 text-sky-300" />
                <div class="min-w-0 flex-1">
                  <p class="text-sm leading-snug">
                    {{ uploadState.pausedByUser ? 'Upload paused' : 'Upload paused: no connection' }}
                  </p>
                  <p class="text-[11px] text-muted-foreground">
                    <template v-if="uploadState.pausedByUser">
                      Resume continues with the files not uploaded yet. Files that were uploading start again from the beginning.
                    </template>
                    <template v-else>
                      {{ uploadState.paused }} · Resuming skips everything already uploaded.
                    </template>
                    <template v-if="uploadState.queue.length">
                      {{ uploadState.queue.length }} more upload(s) wait in the queue.
                    </template>
                  </p>
                </div>
              </div>
              <div class="grid grid-cols-2 gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  class="cursor-pointer select-none"
                  @click="uploadManager.cancelUpload()"
                >
                  Cancel
                </Button>
                <Button
                  size="sm"
                  class="cursor-pointer select-none"
                  :disabled="uploadState.resuming"
                  @click="uploadManager.resumeUpload()"
                >
                  <Play
                    v-if="uploadState.pausedByUser"
                    class="size-3.5"
                  />
                  <RotateCcw
                    v-else
                    class="size-3.5"
                  />
                  {{ uploadState.resuming ? 'Resuming…' : uploadState.pausedByUser ? 'Resume' : 'Retry now' }}
                </Button>
              </div>
            </div>
            <div
              v-if="resumeOffer"
              class="flex flex-col gap-3 rounded-xl border border-primary/30 bg-primary/10 p-3"
              style="--wails-draggable: none"
            >
              <div class="flex gap-2.5">
                <RotateCcw class="mt-0.5 size-4 shrink-0 text-primary" />
                <div class="min-w-0 flex-1">
                  <p class="text-sm leading-snug">
                    Interrupted upload<span v-if="resumeTitle">: {{ resumeTitle }}</span>
                  </p>
                  <p class="text-[11px] text-muted-foreground">
                    {{ resumeDetail }}
                  </p>
                </div>
              </div>
              <div class="grid grid-cols-2 gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  class="cursor-pointer select-none"
                  @click="discardResume"
                >
                  Discard
                </Button>
                <Button
                  size="sm"
                  class="cursor-pointer select-none"
                  @click="acceptResume"
                >
                  <RotateCcw class="size-3.5" />
                  Resume upload
                </Button>
              </div>
            </div>
            <div
              class="flex flex-col items-center justify-center rounded-2xl border border-dashed border-muted-foreground/30 bg-card/40 px-6 text-center"
              :class="hasResults ? 'py-6' : 'min-h-56 flex-1 py-8'"
            >
              <div class="mb-3 flex size-14 items-center justify-center rounded-2xl bg-primary/15 text-primary ring-1 ring-primary/25">
                <UploadCloud class="size-7" />
              </div>
              <h1 class="text-lg font-semibold tracking-tight select-none">
                Drop files to upload
              </h1>
              <p class="mt-1 max-w-64 text-sm text-muted-foreground select-none">
                Drag photos, videos or folders anywhere onto this window.
              </p>
              <div
                v-if="prefs"
                class="mt-4 flex flex-wrap justify-center gap-1.5 select-none"
              >
                <span class="chip">{{ prefs.saver ? 'Storage saver' : 'Original quality' }}</span>
                <span class="chip">{{ prefs.uploadThreads }} {{ prefs.uploadThreads === 1 ? 'thread' : 'threads' }}</span>
                <span
                  v-if="prefs.recursive"
                  class="chip"
                >Recursive</span>
                <span
                  v-if="prefs.pairLivePhotos"
                  class="chip"
                >Live Photos</span>
                <span
                  v-if="prefs.forceUpload"
                  class="chip"
                >Force upload</span>
                <span
                  v-if="prefs.proxy"
                  class="chip"
                >Proxy</span>
                <span
                  v-if="prefs.deleteFromHost"
                  class="chip chip-danger"
                >Deletes local files</span>
              </div>
            </div>

            <div
              v-if="tokenBindingEmail"
              class="flex flex-col gap-3 rounded-xl border border-amber-500/30 bg-amber-500/10 p-3"
              style="--wails-draggable: none"
            >
              <div class="flex gap-2.5">
                <KeyRound class="mt-0.5 size-4 shrink-0 text-amber-300" />
                <p class="text-sm leading-snug text-amber-100/90">
                  This credential needs a token binding key from the rooted Android device it was captured from.
                </p>
              </div>
              <Button
                variant="outline"
                class="cursor-pointer select-none"
                :disabled="isExtractingTokenBinding"
                @click="addTokenBindingAliasFromADB"
              >
                {{ isExtractingTokenBinding ? 'Reading ADB...' : 'Read from ADB' }}
              </Button>
            </div>

            <div
              v-if="hasResults"
              class="flex flex-col gap-3 rounded-xl border bg-card p-3"
              style="--wails-draggable: none"
            >
              <div class="flex items-baseline justify-between gap-2 px-1">
                <h2 class="text-sm font-semibold select-none">
                  Last upload
                </h2>
                <span class="text-xs text-muted-foreground tabular-nums">{{ lastBatchSummary }}</span>
              </div>
              <div class="grid grid-cols-4 gap-1.5 text-center select-none">
                <div class="rounded-lg bg-primary/10 px-1 py-2">
                  <p class="text-lg leading-none font-semibold text-primary tabular-nums">
                    {{ uploadState.results.success.length }}
                  </p>
                  <p class="mt-1 text-[10px] text-muted-foreground">
                    Uploaded
                  </p>
                </div>
                <div class="rounded-lg bg-muted/60 px-1 py-2">
                  <p class="text-lg leading-none font-semibold tabular-nums">
                    {{ uploadState.results.skipped.length }}
                  </p>
                  <p class="mt-1 text-[10px] text-muted-foreground">
                    Skipped
                  </p>
                </div>
                <div
                  class="rounded-lg px-1 py-2"
                  :class="uploadState.results.fail.length ? 'bg-destructive/15' : 'bg-muted/60'"
                >
                  <p
                    class="text-lg leading-none font-semibold tabular-nums"
                    :class="uploadState.results.fail.length ? 'text-destructive' : ''"
                  >
                    {{ uploadState.results.fail.length }}
                  </p>
                  <p class="mt-1 text-[10px] text-muted-foreground">
                    Failed
                  </p>
                </div>
                <div
                  class="rounded-lg px-1 py-2"
                  :class="uploadState.results.warnings.length ? 'bg-amber-500/15' : 'bg-muted/60'"
                >
                  <p
                    class="text-lg leading-none font-semibold tabular-nums"
                    :class="uploadState.results.warnings.length ? 'text-amber-300' : ''"
                  >
                    {{ uploadState.results.warnings.length }}
                  </p>
                  <p class="mt-1 text-[10px] text-muted-foreground">
                    Warnings
                  </p>
                </div>
              </div>
              <div class="grid grid-cols-2 gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  class="cursor-pointer select-none"
                  @click="handleCopyClick"
                >
                  <component :is="copyButtonText === 'Copied!' ? Check : Copy" />
                  {{ copyButtonText }}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  class="cursor-pointer select-none"
                  :aria-expanded="showResultDetails"
                  @click="showResultDetails = !showResultDetails"
                >
                  <ChevronDown
                    class="transition-transform"
                    :class="showResultDetails && 'rotate-180'"
                  />
                  {{ showResultDetails ? 'Hide details' : 'Details' }}
                </Button>
              </div>

              <ul
                v-if="showResultDetails"
                class="max-h-64 divide-y overflow-y-auto rounded-lg border md:max-h-[45vh]"
              >
                <li
                  v-for="(row, index) in resultRows"
                  :key="index"
                  class="flex gap-2 px-2.5 py-1.5"
                >
                  <MediaThumb
                    v-if="thumbnailsActive && row.path"
                    :path="row.path"
                    cached-only
                    class="size-8"
                  />
                  <CircleX
                    v-if="row.kind === 'fail'"
                    class="mt-0.5 size-3.5 shrink-0 text-destructive"
                  />
                  <AlertTriangle
                    v-else-if="row.kind === 'warning'"
                    class="mt-0.5 size-3.5 shrink-0 text-amber-300"
                  />
                  <SkipForward
                    v-else-if="row.kind === 'skipped'"
                    class="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
                  />
                  <CircleCheck
                    v-else
                    class="mt-0.5 size-3.5 shrink-0 text-primary"
                  />
                  <div class="min-w-0 flex-1">
                    <p
                      class="text-xs leading-snug break-words select-text"
                      :class="row.kind === 'fail' && 'text-destructive'"
                    >
                      {{ row.title }}
                    </p>
                    <p
                      v-if="row.detail"
                      class="text-[11px] leading-snug text-muted-foreground"
                    >
                      {{ row.detail }}
                    </p>
                    <p
                      v-if="debugEnabled && row.technical"
                      class="mt-0.5 font-mono text-[10px] leading-relaxed break-all whitespace-pre-line text-muted-foreground select-text"
                    >
                      {{ row.technical }}
                    </p>
                  </div>
                  <button
                    v-if="row.mediaKey"
                    type="button"
                    class="flex size-6 shrink-0 cursor-pointer items-center justify-center self-start rounded text-muted-foreground hover:bg-accent hover:text-foreground"
                    title="Open in Google Photos"
                    :aria-label="`Open ${row.title} in Google Photos`"
                    @click="openInGooglePhotos(row.mediaKey)"
                  >
                    <ExternalLink class="size-3.5" />
                  </button>
                </li>
                <li
                  v-if="allResultRows.length > resultRows.length"
                  class="px-2.5 py-1.5 text-[11px] text-muted-foreground"
                >
                  + {{ allResultRows.length - resultRows.length }} more. Copy as JSON for the full list.
                </li>
              </ul>
            </div>
          </template>
        </template>
      </div>
      <!-- Kept mounted while the drop zones or album prompt cover it -->
      <div
        v-if="isActive"
        v-show="!showDropZones && !showAlbumInput && page === 'upload'"
        class="min-h-0 w-full flex-1"
      >
        <Upload />
      </div>

      <!-- The other pages -->
      <div
        v-if="page !== 'upload' && !showDropZones && !showAlbumInput"
        class="min-h-0 flex-1 overflow-y-auto"
        style="--wails-draggable: none"
      >
        <div class="mx-auto w-full max-w-3xl px-4 pt-3 pb-6">
          <p
            v-if="pageDescriptions[page]"
            class="mb-4 text-xs leading-snug text-muted-foreground select-none"
          >
            {{ pageDescriptions[page] }}
          </p>
          <KeepAlive>
            <StoragePage v-if="page === 'storage'" />
            <BinPage v-else-if="page === 'bin'" />
            <StatsPanel
              v-else-if="page === 'live'"
              view="live"
            />
            <ActivityPage v-else-if="page === 'activity'" />
            <SettingsPanel v-else-if="page === 'settings'" />
          </KeepAlive>
        </div>
      </div>

      <!-- Status bar (debug mode) -->
      <button
        v-if="debugEnabled"
        type="button"
        class="flex h-6 w-full shrink-0 cursor-pointer items-center gap-3 border-t bg-muted/40 px-3 font-mono text-[10px] text-muted-foreground select-none hover:text-foreground"
        style="--wails-draggable: none"
        title="Open debug panel"
        @click="isDebugOpen = true"
      >
        <span class="flex items-center gap-1.5">
          <span
            class="size-1.5 rounded-full"
            :class="isActive ? 'animate-pulse bg-sky-400' : 'bg-primary'"
          />
          {{ isActive ? 'uploading' : 'idle' }}
        </span>
        <span
          v-if="uploadState.queue.length"
          class="tabular-nums"
        >
          queued {{ uploadState.queue.length }}
        </span>
        <span class="tabular-nums">events {{ totalEventCount }}</span>
        <span class="tabular-nums">accounts {{ options.length }}</span>
        <span class="ml-auto tabular-nums">{{ viewport }}</span>
      </button>
    </div>

    <Sheet v-model:open="isCloseDialogOpen">
      <SheetContent
        side="bottom"
        class="gap-0"
        style="--wails-draggable: none"
      >
        <div class="mx-auto flex w-full max-w-md flex-col gap-4 px-5 pt-5 pb-5">
          <SheetHeader class="gap-1.5 p-0">
            <SheetTitle>Keep gotohp running?</SheetTitle>
            <SheetDescription class="text-sm leading-snug">
              <template v-if="isActive">
                An upload is running. In the tray it keeps going; quitting stops it, and you can resume it the next time you open gotohp.
              </template>
              <template v-else-if="watchFoldersActive">
                Watch folders only upload while gotohp is running. In the tray it keeps watching.
              </template>
              <template v-else>
                gotohp can stay in the system tray, ready for the next upload, or quit completely.
              </template>
            </SheetDescription>
          </SheetHeader>
          <label class="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground select-none">
            <input
              v-model="rememberCloseChoice"
              type="checkbox"
              class="accent-primary"
            >
            Don't ask again (change it in Settings)
          </label>
          <div class="grid grid-cols-2 gap-2">
            <Button
              variant="outline"
              class="cursor-pointer select-none"
              @click="answerClose('quit')"
            >
              <Power />
              Quit
            </Button>
            <Button
              class="cursor-pointer select-none"
              @click="answerClose('tray')"
            >
              <PanelBottomClose />
              Minimize to tray
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
    <DebugPanel
      v-if="debugEnabled"
      v-model:open="isDebugOpen"
    />
    <GoogleAuthSetup
      v-model:open="isAccountSetupOpen"
      @account-added="refreshCredentials"
    />
    <!-- Bottom right, out of the way of the cursor; every toast closes itself -->
    <Toaster
      position="bottom-right"
      theme="dark"
      rich-colors
      close-button
      :duration="6000"
      :visible-toasts="3"
    />
  </main>
</template>
