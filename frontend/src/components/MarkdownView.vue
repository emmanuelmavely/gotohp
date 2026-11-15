<script setup lang="ts">
import { computed } from 'vue'
import { parseMarkdown } from '@/utils/markdown'
import MarkdownInline from '@/components/MarkdownInline.vue'

const props = defineProps<{
  source: string
}>()

const blocks = computed(() => parseMarkdown(props.source))

const headingClass = {
  1: 'text-base',
  2: 'mt-3 border-b pb-1 text-sm',
  3: 'mt-1 text-[11px] tracking-wider text-muted-foreground uppercase',
} as const
</script>

<template>
  <div class="flex flex-col gap-2 text-sm leading-relaxed">
    <template
      v-for="(block, index) in blocks"
      :key="index"
    >
      <component
        :is="`h${block.level + 1}`"
        v-if="block.kind === 'heading'"
        :class="['font-semibold text-foreground', headingClass[block.level]]"
      >
        <MarkdownInline :parts="block.parts" />
      </component>
      <ul
        v-else-if="block.kind === 'list'"
        class="flex list-disc flex-col gap-1 pl-5 text-muted-foreground marker:text-muted-foreground/60"
      >
        <li
          v-for="(item, itemIndex) in block.items"
          :key="itemIndex"
        >
          <MarkdownInline :parts="item" />
        </li>
      </ul>
      <p
        v-else
        class="text-muted-foreground"
      >
        <MarkdownInline :parts="block.parts" />
      </p>
    </template>
  </div>
</template>
