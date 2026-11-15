<script setup lang="ts">
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import MarkdownView from '@/components/MarkdownView.vue'

defineProps<{
  title: string
  description?: string
  /** Markdown to show; empty shows the empty text. */
  source: string
  loading?: boolean
  emptyText?: string
}>()

const open = defineModel<boolean>('open', { required: true })
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent
      side="bottom"
      class="max-h-[90vh] gap-0"
      style="--wails-draggable: none"
      @open-auto-focus.prevent
    >
      <SheetHeader class="mx-auto w-full max-w-2xl px-5 pt-4 pb-3">
        <SheetTitle>{{ title }}</SheetTitle>
        <SheetDescription
          v-if="description"
          class="text-xs"
        >
          {{ description }}
        </SheetDescription>
      </SheetHeader>
      <div class="min-h-0 flex-1 overflow-y-auto">
        <div class="mx-auto w-full max-w-2xl px-5 pb-5">
          <p
            v-if="loading"
            class="text-sm text-muted-foreground"
          >
            Loading…
          </p>
          <MarkdownView
            v-else-if="source.trim()"
            :source="source"
          />
          <p
            v-else
            class="text-sm text-muted-foreground"
          >
            {{ emptyText ?? 'Nothing to show.' }}
          </p>
          <slot name="footer" />
        </div>
      </div>
    </SheetContent>
  </Sheet>
</template>
