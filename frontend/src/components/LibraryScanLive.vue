<script setup lang="ts">
import { useTransition } from '@vueuse/core'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { libraryScan } from '@/utils/accountStorage'
import { formatBytes, formatDuration } from '@/utils/format'

/** Points kept for the growth line. */
const MAX_HISTORY = 80
/** viewBox of the growth line; the SVG stretches to the card width. */
const CHART_WIDTH = 300
const CHART_HEIGHT = 44

const progress = computed(() => libraryScan.progress)

// Only changes since the last scan are being fetched: the totals start at the
// previous result instead of climbing from zero.
const isDelta = computed(() => progress.value !== null && progress.value.Pages === 0)

const countedBytes = computed(() =>
  progress.value ? (progress.value.UniqueChargedBytes ?? progress.value.ChargedBytes) : 0,
)
const countedFiles = computed(() =>
  progress.value ? (progress.value.UniqueChargedItems ?? progress.value.ChargedItems) : 0,
)
const items = computed(() => progress.value?.Items ?? 0)
const libraryBytes = computed(() => progress.value?.TotalBytes ?? 0)

// Numbers glide to each new page's totals instead of jumping.
const tween = { duration: 700 }
const shownBytes = useTransition(countedBytes, tween)
const shownFiles = useTransition(countedFiles, tween)
const shownItems = useTransition(items, tween)
const shownLibraryBytes = useTransition(libraryBytes, tween)

// --- Elapsed time, rate, estimate -------------------------------------------
const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  ticker = setInterval(() => now.value = Date.now(), 1000)
})
onUnmounted(() => {
  if (ticker) clearInterval(ticker)
})

const elapsedSeconds = computed(() =>
  libraryScan.startedAt > 0 ? Math.max(0, (now.value - libraryScan.startedAt) / 1000) : 0,
)

const itemsPerSecond = computed(() =>
  !isDelta.value && elapsedSeconds.value >= 2 ? items.value / elapsedSeconds.value : 0,
)

/** Share of the library read, 0..1, or null when the size cannot be estimated. */
const fraction = computed<number | null>(() => {
  if (isDelta.value || libraryScan.expectedItems <= 0 || !progress.value) return null
  // The library may have grown since the last scan, so never claim 100%.
  return Math.min(0.99, items.value / libraryScan.expectedItems)
})

const remainingText = computed(() => {
  if (fraction.value === null || itemsPerSecond.value <= 0) return ''
  const left = Math.max(0, libraryScan.expectedItems - items.value)
  return `about ${formatDuration(left / itemsPerSecond.value)} left`
})

// --- Growth line -------------------------------------------------------------
const history = ref<number[]>([])

watch(countedBytes, (bytes) => {
  history.value.push(bytes)
  if (history.value.length > MAX_HISTORY) history.value.shift()
})
watch(() => libraryScan.startedAt, () => history.value = [])

const linePoints = computed(() => {
  const values = history.value
  if (values.length < 2) return ''
  const max = Math.max(...values, 1)
  const step = CHART_WIDTH / (values.length - 1)
  return values
    .map((value, index) => `${(index * step).toFixed(1)},${(CHART_HEIGHT - 2 - (value / max) * (CHART_HEIGHT - 6)).toFixed(1)}`)
    .join(' ')
})

const areaPoints = computed(() =>
  linePoints.value ? `0,${CHART_HEIGHT} ${linePoints.value} ${CHART_WIDTH},${CHART_HEIGHT}` : '',
)

const headline = computed(() => {
  if (!progress.value) return 'Connecting to your library'
  return isDelta.value ? 'Checking for changes' : 'Scanning your library'
})
</script>

<template>
  <div
    class="scan-card relative overflow-hidden rounded-xl border border-primary/25 bg-primary/5 p-3"
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
      <span
        v-if="progress && !isDelta"
        class="text-muted-foreground tabular-nums"
      >· page {{ progress.Pages.toLocaleString() }}</span>
      <span class="ml-auto text-muted-foreground tabular-nums">{{ formatDuration(elapsedSeconds) }}</span>
    </div>

    <!-- Headline number with the growth line behind it -->
    <div class="relative mt-3">
      <svg
        v-if="linePoints"
        class="pointer-events-none absolute right-0 bottom-0 h-11 w-1/2"
        :viewBox="`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`"
        preserveAspectRatio="none"
        aria-hidden="true"
      >
        <polygon
          :points="areaPoints"
          class="fill-primary/10"
        />
        <polyline
          :points="linePoints"
          class="fill-none stroke-primary/60"
          stroke-width="1.5"
          vector-effect="non-scaling-stroke"
          stroke-linejoin="round"
        />
      </svg>
      <p class="relative text-3xl leading-none font-semibold tracking-tight tabular-nums">
        {{ formatBytes(shownBytes, 2) }}
      </p>
      <p class="relative mt-1.5 text-xs text-muted-foreground tabular-nums">
        counted so far, from {{ Math.round(shownFiles).toLocaleString() }} files
      </p>
    </div>

    <!-- Progress: a real bar when the library size is known from an earlier scan -->
    <div
      class="mt-3 h-1.5 overflow-hidden rounded-full bg-primary/15"
      role="progressbar"
      :aria-valuenow="fraction === null ? undefined : Math.round(fraction * 100)"
      aria-valuemin="0"
      aria-valuemax="100"
    >
      <div
        v-if="fraction !== null"
        class="h-full rounded-full bg-primary transition-[width] duration-700 ease-out"
        :style="{ width: `${fraction * 100}%` }"
      />
      <div
        v-else
        class="scan-sweep h-full w-1/3 rounded-full bg-primary"
      />
    </div>
    <p class="mt-1.5 flex justify-between gap-2 text-[11px] text-muted-foreground tabular-nums">
      <span v-if="fraction !== null">
        about {{ Math.round(fraction * 100) }}% of {{ libraryScan.expectedItems.toLocaleString() }} items from your last scan
      </span>
      <span v-else-if="isDelta">Only changes since your last scan are fetched.</span>
      <span v-else>Google doesn't say how big the library is, so there is no percentage on a first scan.</span>
      <span class="shrink-0">{{ remainingText }}</span>
    </p>

    <!-- Detail tiles -->
    <div class="mt-3 grid grid-cols-3 gap-1.5">
      <div class="rounded-lg bg-background/50 px-2.5 py-2">
        <p class="text-[10px] tracking-wide text-muted-foreground uppercase">
          Items read
        </p>
        <p class="mt-0.5 truncate text-sm font-medium tabular-nums">
          {{ Math.round(shownItems).toLocaleString() }}
        </p>
      </div>
      <div class="rounded-lg bg-background/50 px-2.5 py-2">
        <p class="text-[10px] tracking-wide text-muted-foreground uppercase">
          Their size
        </p>
        <p class="mt-0.5 truncate text-sm font-medium tabular-nums">
          {{ formatBytes(shownLibraryBytes, 2) }}
        </p>
      </div>
      <div class="rounded-lg bg-background/50 px-2.5 py-2">
        <p class="text-[10px] tracking-wide text-muted-foreground uppercase">
          Speed
        </p>
        <p class="mt-0.5 truncate text-sm font-medium tabular-nums">
          {{ itemsPerSecond > 0 ? `${Math.round(itemsPerSecond).toLocaleString()} items/s` : '--' }}
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.scan-sweep {
  animation: scan-sweep 1.4s ease-in-out infinite;
}

@keyframes scan-sweep {
  0% {
    transform: translateX(-100%);
  }

  100% {
    transform: translateX(300%);
  }
}

/* A soft light passing over the card while the scan runs */
.scan-card::before {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(105deg, transparent 40%, oklch(0.696 0.17 162.48 / 9%) 50%, transparent 60%);
  transform: translateX(-100%);
  animation: scan-glint 2.8s ease-in-out infinite;
  pointer-events: none;
}

@keyframes scan-glint {
  to {
    transform: translateX(100%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .scan-sweep,
  .scan-card::before {
    animation: none;
  }

  .scan-sweep {
    width: 100%;
    opacity: 0.4;
  }
}
</style>
