<script setup lang="ts">
import { computed } from 'vue'
import { useColorMode } from '@vueuse/core'
import { formatBytes } from '@/utils/format'

const props = withDefaults(defineProps<{
  /** Network throughput samples (bytes/s), one per second. */
  network: number[]
  /** Local disk read throughput samples (bytes/s), one per second. */
  disk: number[]
  networkLabel?: string
  diskLabel?: string
}>(), { networkLabel: 'Upload', diskLabel: 'Disk read' })

const WIDTH = 600
const HEIGHT = 120
const PADDING = 4

const colorMode = useColorMode()
const isDark = computed(() => colorMode.value === 'dark')

// Both lines share one y scale so their heights are comparable.
const maxValue = computed(() => {
  let max = 0
  for (const value of props.network) if (value > max) max = value
  for (const value of props.disk) if (value > max) max = value
  return max
})

const strokeColors = computed(() =>
  isDark.value
    ? { network: '#38bdf8', disk: '#a78bfa', grid: 'rgba(148,163,184,0.15)' }
    : { network: '#0284c7', disk: '#7c3aed', grid: 'rgba(100,116,139,0.2)' },
)

/** Maps a sample series onto the SVG viewBox coordinates, newest at the right. */
function linePoints(samples: number[]): string {
  const count = samples.length
  if (count < 2) return ''
  const usableWidth = WIDTH - PADDING * 2
  const usableHeight = HEIGHT - PADDING * 2
  const step = usableWidth / (count - 1)
  return samples
    .map((value, index) => {
      const x = PADDING + index * step
      const y = HEIGHT - PADDING - (maxValue.value > 0 ? (value / maxValue.value) * usableHeight : 0)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

const networkPoints = computed(() => linePoints(props.network))
const diskPoints = computed(() => linePoints(props.disk))

const scaleLabel = computed(() => formatBytes(maxValue.value))
</script>

<template>
  <div class="rounded-lg border bg-card px-3 py-2">
    <div class="mb-1 flex items-center justify-between text-[11px]">
      <div class="flex items-center gap-3">
        <span class="flex items-center gap-1.5">
          <span
            class="inline-block size-2 rounded-full"
            :style="{ backgroundColor: strokeColors.network }"
          />
          <span class="text-muted-foreground">{{ networkLabel }}</span>
        </span>
        <span class="flex items-center gap-1.5">
          <span
            class="inline-block size-2 rounded-full"
            :style="{ backgroundColor: strokeColors.disk }"
          />
          <span class="text-muted-foreground">{{ diskLabel }}</span>
        </span>
      </div>
      <span class="text-muted-foreground tabular-nums">peak {{ scaleLabel }}/s</span>
    </div>
    <svg
      :viewBox="`0 0 ${WIDTH} ${HEIGHT}`"
      preserveAspectRatio="none"
      class="block h-24 w-full"
      role="img"
      :aria-label="`${networkLabel} and ${diskLabel} throughput over the last two minutes`"
    >
      <line
        :x1="PADDING"
        :y1="HEIGHT / 2"
        :x2="WIDTH - PADDING"
        :y2="HEIGHT / 2"
        :stroke="strokeColors.grid"
        stroke-width="1"
        stroke-dasharray="4 4"
      />
      <polyline
        :points="diskPoints"
        fill="none"
        :stroke="strokeColors.disk"
        stroke-width="1.5"
        stroke-linejoin="round"
        vector-effect="non-scaling-stroke"
      />
      <polyline
        :points="networkPoints"
        fill="none"
        :stroke="strokeColors.network"
        stroke-width="1.5"
        stroke-linejoin="round"
        vector-effect="non-scaling-stroke"
      />
    </svg>
  </div>
</template>
