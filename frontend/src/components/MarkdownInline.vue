<script setup lang="ts">
import type { InlinePart } from '@/utils/markdown'
import { openExternal } from '@/utils/appUpdate'

defineProps<{
  parts: InlinePart[]
}>()
</script>

<template>
  <template
    v-for="(part, index) in parts"
    :key="index"
  >
    <code
      v-if="part.kind === 'code'"
      class="rounded bg-muted px-1 py-px font-mono text-[0.85em] text-foreground"
    >{{ part.text }}</code>
    <strong
      v-else-if="part.kind === 'bold'"
      class="font-semibold text-foreground"
    >{{ part.text }}</strong>
    <a
      v-else-if="part.kind === 'link'"
      :href="part.url"
      class="break-all text-primary underline-offset-2 hover:underline"
      @click.prevent="openExternal(part.url)"
    >{{ part.text }}</a>
    <template v-else>
      {{ part.text }}
    </template>
  </template>
</template>
