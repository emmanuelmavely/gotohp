<script setup lang="ts">
import { useTransition } from '@vueuse/core'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Check, Download, Link2, ScanSearch, Trash2, Upload, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { formatBytes, formatDuration } from '@/utils/format'
import { cancelLivePhotoRepair, liveRepair, REPAIR_STAGES, repairFraction } from '@/utils/liveRepair'

const STEP_INFO: Record<string, { label: string, icon: typeof Download }> = {
  downloading: { label: 'Download', icon: Download },
  verifying: { label: 'Verify', icon: ScanSearch },
  uploading: { label: 'Upload', icon: Upload },
  linking: { label: 'Link', icon: Link2 },
  trashing: { label: 'Trash copy', icon: Trash2 },
}

const progress = computed(() => liveRepair.progress)

// Before the first pair starts, the library is checked for changes.
const headline = computed(() => {
  if (liveRepair.cancelling) return 'Stopping…'
  if (!progress.value) return 'Checking the library'
  return `Repairing ${progress.value.Index} of ${progress.value.Total}`
})

const fraction = computed(() => repairFraction(progress.value))
const shownFraction = useTransition(fraction, { duration: 500 })

const stageIndex = computed(() => {
  const stage = progress.value?.Stage ?? ''
  return stage === 'done' ? REPAIR_STAGES.length : REPAIR_STAGES.indexOf(stage as typeof REPAIR_STAGES[number])
})

const isTransferring = computed(() =>
  progress.value?.Stage === 'downloading' || progress.value?.Stage === 'uploading',
)

const counts = computed(() => {
  const result = { repaired: 0, skipped: 0, failed: 0 }
  for (const outcome of liveRepair.outcomes) {
    if (outcome.Status === 'repaired') result.repaired++
    else if (outcome.Status === 'skipped') result.skipped++
    else result.failed++
  }
  return result
})

// --- Transfer speed ------------------------------------------------------------
const speed = ref(0)
let lastSample: { at: number, bytes: number, stage: string } | null = null

watch(progress, (current) => {
  if (!current || !(current.Stage === 'downloading' || current.Stage === 'uploading')) {
    lastSample = null
    speed.value = 0
    return
  }
  const now = Date.now()
  if (lastSample && lastSample.stage === current.Stage && current.BytesDone >= lastSample.bytes && now > lastSample.at) {
    const instant = ((current.BytesDone - lastSample.bytes) / (now - lastSample.at)) * 1000
    speed.value = speed.value > 0 ? speed.value * 0.7 + instant * 0.3 : instant
  }
  lastSample = { at: now, bytes: current.BytesDone, stage: current.Stage }
})

// --- Elapsed time ----------------------------------------------------------------
const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  ticker = setInterval(() => now.value = Date.now(), 1000)
})
onUnmounted(() => {
  if (ticker) clearInterval(ticker)
})
const elapsed = computed(() => (liveRepair.startedAt > 0 ? (now.value - liveRepair.startedAt) / 1000 : 0))
</script>

<template>
  <div
    class="repair-card relative overflow-hidden rounded-xl border border-primary/25 bg-primary/5 p-3"
    role="status"
    aria-live="off"
  >
    <!-- Status line -->
    <div class="flex items-center gap-2 text-xs">
      <span class="relative flex size-2 shrink-0">
        <span class="absolute inline-flex size-full animate-ping rounded-full bg-primary opacity-60" />
        <span class="relative inline-flex size-2 rounded-full bg-primary" />
      </span>
      <span class="font-medium">{{ headline }}</span>
      <span class="ml-auto text-muted-foreground tabular-nums">{{ formatDuration(elapsed) }}</span>
      <Button
        variant="outline"
        size="sm"
        class="h-7 cursor-pointer px-2 text-xs"
        :disabled="liveRepair.cancelling"
        @click="cancelLivePhotoRepair"
      >
        <X class="size-3.5" />
        {{ liveRepair.cancelling ? 'Stopping…' : 'Cancel' }}
      </Button>
    </div>

    <!-- Current pair -->
    <p
      v-if="progress"
      class="mt-2.5 truncate text-sm font-medium"
      :title="progress.FileName"
    >
      {{ progress.FileName }}
    </p>

    <!-- Stage stepper -->
    <ol
      v-if="progress"
      class="mt-2 grid grid-cols-5 gap-1"
    >
      <li
        v-for="(stage, index) in REPAIR_STAGES"
        :key="stage"
        class="flex flex-col items-center gap-1 text-center"
      >
        <span
          class="flex size-7 items-center justify-center rounded-full border transition-colors duration-300"
          :class="index < stageIndex
            ? 'border-primary bg-primary text-primary-foreground'
            : index === stageIndex
              ? 'border-primary bg-primary/15 text-primary'
              : 'border-border bg-background/50 text-muted-foreground'"
        >
          <Check
            v-if="index < stageIndex"
            class="size-3.5"
          />
          <component
            :is="STEP_INFO[stage].icon"
            v-else
            class="size-3.5"
            :class="index === stageIndex && 'animate-pulse'"
          />
        </span>
        <span
          class="text-[10px] leading-tight"
          :class="index === stageIndex ? 'font-medium text-foreground' : 'text-muted-foreground'"
        >{{ STEP_INFO[stage].label }}</span>
      </li>
    </ol>

    <!-- Bytes of the download or upload in progress -->
    <div
      v-if="progress && isTransferring"
      class="mt-2.5"
    >
      <div class="h-1 overflow-hidden rounded-full bg-primary/15">
        <div
          class="h-full rounded-full bg-primary/70 transition-[width] duration-300 ease-out"
          :style="{ width: `${progress.BytesTotal > 0 ? Math.min(100, (progress.BytesDone / progress.BytesTotal) * 100) : 0}%` }"
        />
      </div>
      <p class="mt-1 flex justify-between text-[11px] text-muted-foreground tabular-nums">
        <span>
          {{ formatBytes(progress.BytesDone) }}<template v-if="progress.BytesTotal > 0"> of {{ formatBytes(progress.BytesTotal) }}</template>
        </span>
        <span v-if="speed > 0">{{ formatBytes(speed) }}/s</span>
      </p>
    </div>

    <!-- Whole batch -->
    <div class="mt-3 border-t border-primary/15 pt-2.5">
      <div
        class="h-1.5 overflow-hidden rounded-full bg-primary/15"
        role="progressbar"
        :aria-valuenow="Math.round(fraction * 100)"
        aria-valuemin="0"
        aria-valuemax="100"
      >
        <div
          class="h-full rounded-full bg-primary"
          :style="{ width: `${shownFraction * 100}%` }"
        />
      </div>
      <p class="mt-1.5 flex justify-between gap-2 text-[11px] text-muted-foreground tabular-nums">
        <span>{{ Math.round(fraction * 100) }}% of the batch</span>
        <span>
          <span class="text-primary">{{ counts.repaired }} repaired</span>
          · {{ counts.skipped }} skipped
          · <span :class="counts.failed > 0 && 'text-destructive'">{{ counts.failed }} failed</span>
        </span>
      </p>
    </div>
  </div>
</template>

<style scoped>
.repair-card::before {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(105deg, transparent 40%, oklch(0.696 0.17 162.48 / 9%) 50%, transparent 60%);
  transform: translateX(-100%);
  animation: repair-glint 2.8s ease-in-out infinite;
  pointer-events: none;
}

@keyframes repair-glint {
  to {
    transform: translateX(100%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .repair-card::before {
    animation: none;
  }
}
</style>
