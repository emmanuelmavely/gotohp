<script setup lang="ts">
import { computed, onActivated, onMounted, ref } from 'vue'
import { toast } from 'vue-sonner'
import { AlertTriangle, CircleCheck, Images, RotateCcw, Trash2 } from '@lucide/vue'
import * as guiBindings from '../../bindings/app/internal/gui'
import { Button } from '@/components/ui/button'
import StatsPanel from '@/StatsPanel.vue'
import { albumStore } from '@/utils/albums'
import { baseName, formatBytes, formatDuration } from '@/utils/format'
import { uploadManager } from '@/utils/UploadManager'

interface UploadFailure {
  Path: string
  Message: string
}
interface UploadRun {
  StartedAt: number
  FinishedAt: number
  Uploaded: number
  Skipped: number
  Failed: number
  Stopped: number
  Bytes: number
  Album: string
  AlbumAuto: boolean
  AlbumError?: string
  Failures: UploadFailure[] | null
}

// Reached by name so older bindings still build.
const api = (guiBindings as unknown as {
  LibraryService?: {
    UploadHistory?: () => Promise<UploadRun[] | null>
    ClearUploadHistory?: () => Promise<void>
  }
}).LibraryService

const tab = ref<'overview' | 'history' | 'problems'>('overview')
const runs = ref<UploadRun[]>([])
const statsKey = ref(0)

async function load() {
  try {
    runs.value = (await api?.UploadHistory?.()) ?? []
  } catch {
    runs.value = []
  }
}
onMounted(load)
// The page is kept alive between visits; show runs finished meanwhile.
onActivated(() => {
  void load()
  statsKey.value++
})

// Failures before this time were retried or dismissed.
const HANDLED_KEY = 'gotohp.problemsHandledAt'
function readHandled(): number {
  try {
    return Number(localStorage.getItem(HANDLED_KEY)) || 0
  } catch {
    return 0
  }
}
const handledAt = ref(readHandled())
function markHandled() {
  handledAt.value = Date.now()
  try {
    localStorage.setItem(HANDLED_KEY, String(handledAt.value))
  } catch {
    // Without storage the list clears for this session only.
  }
}

/** The newest failure of each file, from runs since the last retry. */
const problems = computed(() => {
  const seen = new Set<string>()
  const list: (UploadFailure & { at: number })[] = []
  for (const run of runs.value) {
    if (run.FinishedAt <= handledAt.value) continue
    for (const failure of run.Failures ?? []) {
      if (!failure.Path || seen.has(failure.Path)) continue
      seen.add(failure.Path)
      list.push({ ...failure, at: run.FinishedAt })
    }
  }
  return list
})

const albumProblems = computed(() => runs.value.filter(run => run.AlbumError && run.FinishedAt > handledAt.value))

function albumTitle(run: UploadRun): string {
  if (run.AlbumAuto) return 'Album per folder'
  if (!run.Album) return ''
  const known = albumStore.albums.find(album => album.MediaKey === run.Album)
  if (known) return known.Title
  return run.Album.startsWith('AF1Qip') ? 'an album' : run.Album
}

function when(ms: number): string {
  const date = new Date(ms)
  const today = new Date()
  const time = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (date.toDateString() === today.toDateString()) return `Today ${time}`
  return `${date.toLocaleDateString([], { day: 'numeric', month: 'short' })} ${time}`
}

const uploadBusy = computed(() => uploadManager.state.isUploading || uploadManager.state.isStarting)

async function retryAll() {
  const files = problems.value.map(problem => problem.Path)
  if (files.length === 0) return
  try {
    const outcome = await uploadManager.enqueue({ files, albumName: '', albumAutoMode: false })
    markHandled()
    toast.success(outcome === 'queued' ? 'Retry added to the queue' : `Retrying ${files.length} file(s)`)
  } catch (error) {
    toast.error('Could not start the retry', { description: error instanceof Error ? error.message : String(error) })
  }
}

async function clearHistory() {
  try {
    await api?.ClearUploadHistory?.()
    runs.value = []
  } catch (error) {
    toast.error('Could not clear the history', { description: error instanceof Error ? error.message : String(error) })
  }
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <div
      class="flex gap-1 rounded-lg border bg-card p-1 text-sm"
      role="tablist"
    >
      <button
        v-for="item in ([['overview', 'Overview'], ['history', 'History'], ['problems', 'Problems']] as const)"
        :key="item[0]"
        type="button"
        role="tab"
        :aria-selected="tab === item[0]"
        class="flex flex-1 cursor-pointer items-center justify-center gap-1.5 rounded-md px-2 py-1.5 select-none"
        :class="tab === item[0] ? 'bg-primary/15 text-foreground' : 'text-muted-foreground hover:text-foreground'"
        @click="tab = item[0]"
      >
        {{ item[1] }}
        <span
          v-if="item[0] === 'problems' && problems.length"
          class="rounded-full bg-destructive/20 px-1.5 text-[10px] text-destructive tabular-nums"
        >{{ problems.length }}</span>
      </button>
    </div>

    <StatsPanel
      v-if="tab === 'overview'"
      :key="statsKey"
      view="activity"
    />

    <!-- History -->
    <template v-else-if="tab === 'history'">
      <ul
        v-if="runs.length"
        class="divide-y overflow-hidden rounded-xl border bg-card"
      >
        <li
          v-for="run in runs"
          :key="run.StartedAt"
          class="px-3 py-2.5 text-sm"
        >
          <div class="flex items-baseline gap-2">
            <span class="font-medium">{{ when(run.StartedAt) }}</span>
            <span class="text-xs text-muted-foreground tabular-nums">
              {{ formatDuration(Math.max(0, (run.FinishedAt - run.StartedAt) / 1000)) }}
              <template v-if="run.Bytes"> · {{ formatBytes(run.Bytes) }}</template>
            </span>
          </div>
          <p class="mt-0.5 text-xs text-muted-foreground tabular-nums">
            <span class="text-primary">{{ run.Uploaded }} uploaded</span>
            <template v-if="run.Skipped">
              · {{ run.Skipped }} already there
            </template>
            <template v-if="run.Failed">
              · <span class="text-destructive">{{ run.Failed }} failed</span>
            </template>
            <template v-if="run.Stopped">
              · {{ run.Stopped }} stopped
            </template>
          </p>
          <p
            v-if="albumTitle(run)"
            class="mt-0.5 flex items-center gap-1 text-xs"
            :class="run.AlbumError ? 'text-amber-300' : 'text-muted-foreground'"
          >
            <Images class="size-3 shrink-0" />
            <span class="truncate">{{ albumTitle(run) }}<template v-if="run.AlbumError"> · not added: {{ run.AlbumError }}</template></span>
          </p>
        </li>
      </ul>
      <p
        v-else
        class="px-1 text-sm text-muted-foreground"
      >
        No uploads recorded yet.
      </p>
      <button
        v-if="runs.length"
        type="button"
        class="ml-auto flex cursor-pointer items-center gap-1 px-1 text-xs text-muted-foreground hover:text-foreground"
        @click="clearHistory"
      >
        <Trash2 class="size-3" />
        Clear history
      </button>
    </template>

    <!-- Problems -->
    <template v-else>
      <div
        v-if="problems.length"
        class="rounded-xl border bg-card"
      >
        <div class="flex items-center gap-2 border-b px-3 py-2.5">
          <p class="min-w-0 flex-1 text-sm">
            {{ problems.length }} file(s) failed to upload
          </p>
          <Button
            variant="ghost"
            size="sm"
            class="shrink-0 cursor-pointer"
            @click="markHandled"
          >
            Dismiss
          </Button>
          <Button
            size="sm"
            class="shrink-0 cursor-pointer"
            :disabled="uploadBusy"
            @click="retryAll"
          >
            <RotateCcw class="size-3.5" />
            Retry all
          </Button>
        </div>
        <ul class="max-h-[55vh] divide-y overflow-y-auto">
          <li
            v-for="problem in problems"
            :key="problem.Path"
            class="flex gap-2 px-3 py-2"
          >
            <AlertTriangle class="mt-0.5 size-3.5 shrink-0 text-destructive" />
            <div class="min-w-0 flex-1">
              <p
                class="truncate text-sm"
                :title="problem.Path"
              >
                {{ baseName(problem.Path) }}
              </p>
              <p class="text-[11px] leading-snug break-words text-muted-foreground">
                {{ problem.Message || 'Failed' }} · {{ when(problem.at) }}
              </p>
            </div>
          </li>
        </ul>
      </div>
      <div
        v-for="run in albumProblems"
        :key="`album-${run.StartedAt}`"
        class="flex gap-2 rounded-xl border border-amber-500/30 bg-amber-500/10 px-3 py-2.5 text-sm"
      >
        <Images class="mt-0.5 size-3.5 shrink-0 text-amber-300" />
        <p class="min-w-0 flex-1 leading-snug">
          {{ when(run.StartedAt) }}: {{ run.Uploaded }} file(s) uploaded but not added to {{ albumTitle(run) }}.
          <span class="text-xs text-muted-foreground">{{ run.AlbumError }}</span>
        </p>
      </div>
      <p
        v-if="!problems.length && !albumProblems.length"
        class="flex items-center gap-1.5 px-1 text-sm text-muted-foreground"
      >
        <CircleCheck class="size-4 text-primary" />
        No problems in the recent uploads.
      </p>
    </template>
  </div>
</template>
