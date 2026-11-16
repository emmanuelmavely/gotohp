import { Browser } from '@wailsio/runtime'
import { toast } from 'vue-sonner'
import { debugLog } from './debug'

/**
 * Web address of one item in Google Photos. It opens for whichever Google
 * account the browser is signed in to, so it only works for that account's own
 * items.
 */
export function googlePhotosItemUrl(mediaKey: string): string {
  return `https://photos.google.com/photo/${encodeURIComponent(mediaKey)}`
}

/** Opens an item in Google Photos in the system browser. */
export async function openInGooglePhotos(mediaKey: string) {
  if (!mediaKey) return
  const url = googlePhotosItemUrl(mediaKey)
  debugLog('info', 'frontend', 'open in Google Photos', { mediaKey })
  try {
    await Browser.OpenURL(url)
  } catch (error) {
    toast.error('Could not open the browser', {
      description: error instanceof Error ? error.message : String(error),
    })
  }
}

/** Opens an album in Google Photos in the system browser. */
export async function openAlbumInGooglePhotos(albumKey: string) {
  if (!albumKey) return
  try {
    await Browser.OpenURL(`https://photos.google.com/album/${encodeURIComponent(albumKey)}`)
  } catch (error) {
    toast.error('Could not open the browser', {
      description: error instanceof Error ? error.message : String(error),
    })
  }
}
