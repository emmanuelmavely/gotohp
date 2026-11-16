<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { AlertTriangle, CircleCheck, Pause, Play, RefreshCw } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import LibraryScanLive from '@/components/LibraryScanLive.vue'
import ThroughputGraph from '@/components/ThroughputGraph.vue'
import { formatBytes, formatDuration, formatTimeAgo } from '@/utils/format'
import { libraryScan, minuteClock } from '@/utils/accountStorage'
import { uploadManager } from '@/utils/UploadManager'
import {
  backupFolder,
  checkEarlier,
  chooseBackupFolder,
  loadPlan,
  loadRecorded,
  pauseConvert,
  quotaConvert,
  startConvert,
  uploadWaiting,
} from '@/utils/quotaConvert'

const acknowledged = ref(false)
const plan = computed(() => quotaConvert.plan)
const uploadBusy = computed(() =>
  !quotaConvert.running && (uploadManager.state.isUploading || uploadManager.state.isStarting || Boolean(uploadManager.state.paused)),
)
const libraryBusy = computed(() => libraryScan.startedAt > 0 || libraryScan.repairing)

onMounted(() => {
  // Show what is known at once; read the library only when nothing is.
  if (!quotaConvert.plan) void loadPlan()
  else void loadRecorded()
})

const fraction = computed(() => {
  const progress = quotaConvert.progress
  return progress && progress.Total > 0 ? progress.Done / progress.Total : 0
})
const freedBytes = computed(() => (plan.value ? fraction.value * (quotaConvert.trial ? 0 : plan.value.Bytes) : 0))

const phaseText = computed(() => {
  const progress = quotaConvert.progress
  if (quotaConvert.pausing) return 'Pausing after this batch…'
  if (!progress) return 'Starting…'
  const batch = progress.Batches ? ` · batch ${progress.Batch} of ${progress.Batches}` : ''
  if (progress.Phase === 'downloading') return `Downloading ${progress.FileName}${batch}`
  if (progress.Phase === 'deleting') return `Deleting the counted copy of ${progress.FileName}${batch}`
  return `Uploading again without counting${batch}`
})

// Ticks every second for the elapsed and remaining times.
const now = ref(Date.now())
const ticker = setInterval(() => { now.value = Date.now() }, 1000)
onUnmounted(() => clearInterval(ticker))

const elapsed = computed(() =>
  quotaConvert.startedAt ? formatDuration(Math.max(0, now.value - quotaConvert.startedAt) / 1000) : '',
)

const remaining = computed(() => {
  const progress = quotaConvert.progress
  if (!progress || progress.Done < 1 || !quotaConvert.startedAt) return '…'
  const perFile = (now.value - quotaConvert.startedAt) / progress.Done
  return formatDuration(((progress.Total - progress.Done) * perFile) / 1000)
})

const downloadSpeed = computed(() => {
  const history = quotaConvert.downloadHistory
  const recent = history.slice(-5)
  return recent.length ? recent.reduce((sum, value) => sum + value, 0) / recent.length : 0
})

async function convert(limit = 0) {
  if (!acknowledged.value && !quotaConvert.paused) return
  await startConvert(limit)
  acknowledged.value = false
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <!-- What counts -->
    <div class="rounded-xl border bg-card p-4">
      <LibraryScanLive v-if="quotaConvert.planning" />
      <template v-else-if="plan">
        <div class="flex items-start gap-3">
          <div class="min-w-0 flex-1">
            <p class="text-3xl leading-none font-semibold tracking-tight tabular-nums">
              {{ formatBytes(plan.CountedBytes, 2) }}
            </p>
            <p class="mt-1.5 text-xs text-muted-foreground tabular-nums">
              counted by {{ plan.CountedFiles.toLocaleString() }} files
              <template v-if="quotaConvert.planAt">
                · updated {{ formatTimeAgo(quotaConvert.planAt, minuteClock) }}
              </template>
            </p>
          </div>
          <Button
            variant="ghost"
            size="sm"
            class="shrink-0 cursor-pointer"
            title="Read the library again"
            :disabled="quotaConvert.running || libraryBusy"
            @click="loadPlan"
          >
            <RefreshCw class="size-3.5" />
            Refresh
          </Button>
        </div>
        <div
          v-if="quotaConvert.recorded > 0"
          class="mt-3 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs"
        >
          <CircleCheck class="size-3.5 shrink-0 text-primary" />
          <span v-if="!quotaConvert.confirmed">{{ quotaConvert.recorded.toLocaleString() }} files converted earlier.</span>
          <span
            v-else
            class="tabular-nums"
          >
            {{ quotaConvert.confirmed.converted }} back without counting
            <template v-if="quotaConvert.confirmed.waiting"> · {{ quotaConvert.confirmed.waiting }} waiting to upload</template>
            <template v-if="quotaConvert.confirmed.inBin"> · {{ quotaConvert.confirmed.inBin }} in the bin</template>
            <template v-if="quotaConvert.confirmed.counting"> · {{ quotaConvert.confirmed.counting }} counting again</template>
          </span>
          <button
            type="button"
            class="cursor-pointer text-muted-foreground underline-offset-2 hover:text-foreground hover:underline disabled:opacity-50"
            :disabled="quotaConvert.checking || libraryBusy"
            @click="checkEarlier"
          >
            {{ quotaConvert.checking ? 'Checking…' : 'Check' }}
          </button>
          <button
            v-if="quotaConvert.confirmed?.waiting"
            type="button"
            class="cursor-pointer text-primary underline-offset-2 hover:underline"
            @click="uploadWaiting"
          >
            Upload waiting files
          </button>
        </div>
      </template>
      <div
        v-else
        class="flex flex-col items-center gap-2 py-4 text-center"
      >
        <p class="text-sm text-muted-foreground">
          Reads your library to see which files count against your storage.
        </p>
        <Button
          size="sm"
          class="cursor-pointer"
          :disabled="libraryBusy"
          @click="loadPlan"
        >
          Check storage
        </Button>
      </div>
    </div>

    <!-- Running -->
    <div
      v-if="quotaConvert.running"
      class="rounded-xl border bg-card p-4"
    >
      <div class="flex items-end gap-2">
        <p class="text-3xl leading-none font-semibold tracking-tight tabular-nums">
          {{ Math.round(fraction * 100) }}<span class="text-base text-muted-foreground">%</span>
        </p>
        <p
          v-if="!quotaConvert.trial && plan"
          class="ml-auto text-xs text-muted-foreground tabular-nums"
        >
          {{ formatBytes(freedBytes, 1) }} freed of {{ formatBytes(plan.Bytes, 2) }}
        </p>
        <p
          v-else-if="quotaConvert.progress"
          class="ml-auto text-xs text-muted-foreground tabular-nums"
        >
          {{ quotaConvert.progress.Done }} of {{ quotaConvert.progress.Total }} files
        </p>
      </div>
      <p class="mt-1.5 truncate text-xs text-muted-foreground">
        {{ phaseText }}
      </p>
      <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-primary/15">
        <div
          class="h-full rounded-full bg-primary transition-[width] duration-500"
          :style="{ width: `${fraction * 100}%` }"
        />
      </div>
      <div class="mt-3 grid grid-cols-3 gap-1.5 text-center">
        <div class="rounded-lg bg-muted/60 px-1 py-2">
          <p class="text-sm font-semibold tabular-nums">
            {{ formatBytes(downloadSpeed) }}/s
          </p>
          <p class="mt-0.5 text-[10px] text-muted-foreground">
            Download
          </p>
        </div>
        <div class="rounded-lg bg-muted/60 px-1 py-2">
          <p class="text-sm font-semibold tabular-nums">
            {{ elapsed }}
          </p>
          <p class="mt-0.5 text-[10px] text-muted-foreground">
            Elapsed
          </p>
        </div>
        <div class="rounded-lg bg-muted/60 px-1 py-2">
          <p class="text-sm font-semibold tabular-nums">
            {{ remaining }}
          </p>
          <p class="mt-0.5 text-[10px] text-muted-foreground">
            Remaining
          </p>
        </div>
      </div>
      <ThroughputGraph
        v-if="quotaConvert.downloadHistory.length > 1"
        class="mt-3"
        :network="quotaConvert.uploadHistory"
        :disk="quotaConvert.downloadHistory"
        network-label="Upload"
        disk-label="Download"
      />
      <Button
        variant="outline"
        class="mt-3 w-full cursor-pointer"
        :disabled="quotaConvert.pausing"
        @click="pauseConvert"
      >
        <Pause class="size-4" />
        {{ quotaConvert.pausing ? 'Pausing after this batch…' : 'Pause' }}
      </Button>
      <p class="mt-1.5 text-[11px] leading-snug text-muted-foreground">
        Pause finishes the batch of 20 in flight, so no file is left only on this computer.
      </p>
    </div>

    <!-- Paused -->
    <div
      v-else-if="quotaConvert.paused"
      class="flex items-center gap-3 rounded-xl border border-sky-500/30 bg-sky-500/10 p-4"
    >
      <Pause class="size-4 shrink-0 text-sky-300" />
      <div class="min-w-0 flex-1 text-sm">
        Conversion paused
        <p
          v-if="quotaConvert.result"
          class="text-xs text-muted-foreground tabular-nums"
        >
          {{ quotaConvert.result.Converted.toLocaleString() }} converted so far
        </p>
      </div>
      <Button
        class="shrink-0 cursor-pointer"
        :disabled="uploadBusy || libraryBusy"
        @click="convert()"
      >
        <Play class="size-4" />
        Resume
      </Button>
    </div>

    <!-- Convert -->
    <div
      v-else-if="plan && plan.Files > 0"
      class="rounded-xl border bg-card p-4"
    >
      <div class="flex items-baseline gap-2">
        <p class="font-medium">
          Make it unlimited
        </p>
        <p class="ml-auto text-xs text-muted-foreground tabular-nums">
          frees about {{ formatBytes(plan.Bytes, 2) }} · {{ plan.Files.toLocaleString() }} files
        </p>
      </div>
      <div class="mt-3 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-xs leading-snug text-amber-100/90">
        <p class="flex items-center gap-1.5 font-semibold text-amber-200">
          <AlertTriangle class="size-3.5 shrink-0" />
          Each file is deleted from Google Photos and uploaded again
        </p>
        <ul class="mt-1.5 flex list-disc flex-col gap-1 pl-4">
          <li>Its albums, favourites, descriptions, edits and sharing are lost.</li>
          <li>
            A copy is kept in <span
              class="font-mono"
              :title="backupFolder()"
            >{{ backupFolder() }}</span>
            (needs about {{ formatBytes(plan.DownloadBytes, 1) }}).
            <button
              type="button"
              class="cursor-pointer text-amber-200 underline underline-offset-2"
              @click="chooseBackupFolder"
            >
              Change
            </button>
          </li>
          <li v-if="plan.SaverFiles">
            {{ plan.SaverFiles.toLocaleString() }} storage-saver files go back at the same quality.
          </li>
          <li v-if="plan.LivePhotos">
            {{ plan.LivePhotos.toLocaleString() }} Live Photos stay as they are.
          </li>
          <li>Unofficial method: your own risk.</li>
        </ul>
      </div>
      <label class="mt-3 flex cursor-pointer items-start gap-2 text-sm select-none">
        <input
          v-model="acknowledged"
          type="checkbox"
          class="mt-1 accent-destructive"
        >
        I understand, convert these files
      </label>
      <Button
        variant="destructive"
        class="mt-3 w-full cursor-pointer"
        :disabled="!acknowledged || uploadBusy || libraryBusy"
        @click="convert()"
      >
        {{ uploadBusy ? 'Wait for the current upload to finish' : `Convert ${plan.Files.toLocaleString()} files · free ${formatBytes(plan.Bytes, 2)}` }}
      </Button>
      <button
        type="button"
        class="mx-auto mt-2 block cursor-pointer text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline disabled:pointer-events-none disabled:opacity-50"
        :disabled="!acknowledged || uploadBusy || libraryBusy"
        title="Convert the 10 oldest files, then check that they stopped counting"
        @click="convert(10)"
      >
        Try 10 files first
      </button>
    </div>
    <p
      v-else-if="plan && !quotaConvert.planning"
      class="px-1 text-sm text-muted-foreground"
    >
      Nothing left to convert.
    </p>

    <!-- Last run -->
    <div
      v-if="quotaConvert.result && !quotaConvert.running"
      class="rounded-xl border bg-card p-4 text-xs"
    >
      <p class="flex items-center gap-1.5 text-primary">
        <CircleCheck class="size-3.5" />
        {{ quotaConvert.result.Converted.toLocaleString() }} of {{ quotaConvert.result.Total.toLocaleString() }} files converted.
      </p>
      <ul
        v-if="quotaConvert.result.Problems?.length"
        class="mt-2 max-h-40 divide-y overflow-y-auto rounded-lg border"
      >
        <li
          v-for="problem in quotaConvert.result.Problems"
          :key="problem.MediaKey"
          class="px-2.5 py-1.5"
        >
          <span class="text-foreground">{{ problem.FileName }}</span>
          <span class="text-muted-foreground"> · {{ problem.Message }}</span>
        </li>
      </ul>
    </div>
  </div>
</template>
