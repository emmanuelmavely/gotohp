import { ref, watch } from 'vue'

/** What closing the window does: ask each time, keep running in the tray, or quit. */
export type CloseBehavior = 'ask' | 'tray' | 'quit'

const STORAGE_KEY = 'gotohp.closeBehavior'

function readStored(): CloseBehavior {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    return stored === 'tray' || stored === 'quit' ? stored : 'ask'
  } catch {
    return 'ask'
  }
}

/** User preference, remembered per machine. Defaults to asking. */
export const closeBehavior = ref<CloseBehavior>(readStored())

watch(closeBehavior, (behavior) => {
  try {
    localStorage.setItem(STORAGE_KEY, behavior)
  } catch {
    // Without storage the choice lasts for this session.
  }
})
