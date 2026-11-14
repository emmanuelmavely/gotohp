# Changelog

Notable changes in this fork. The base is gotohp 0.11.0.

## Unreleased

### Added

- Close to tray: closing the window asks whether to minimize to the tray or quit, with a "don't ask again" option and a setting to change it later. Clicking the tray icon reopens the window; the tray menu offers Open and Quit.
- Live library scan: while the storage count or the Live Photo scan runs, the app shows the running total, items read, scan speed and, after a first full scan, an estimated percentage and time left.
- Statistics sheet: storage the account really uses (each file counted once), all-time upload and download totals, average speed and a 30-day upload history.
- Storage strip under the account name with a one-click storage count. The last count is remembered per account between sessions and shows how long ago it was made; Refresh counts again.
- Live Photo repair in the app and as `repair live-photos`: finds photos whose video was uploaded as a separate item and rejoins them after verifying the video.
- Live Photo repair progress: each pair shows its stage (download, verify, upload, link, trash copy) with transfer progress and speed, plus overall progress and running counts. A repair can be cancelled and keeps running while the statistics sheet is closed. `repair live-photos --execute` prints a live line and each result as it happens.
- "Open in Google Photos" links on Live Photo repair results, repair candidates and uploaded files. They open in the system browser, for the Google account it is signed in to.
- Statistics count re-uploaded repair videos as uploaded bytes and show how many Live Photos were repaired.
- `quota list` command, with `--breakdown` and `--json`, and live progress while it scans.
- Library index cache: after the first scan, library scans only fetch changes.
- `upload --dry-run` to report what is already uploaded without uploading.
- Hash cache: files dropped again are not re-hashed.
- Upload journal: an interrupted upload can be resumed on the next start (GUI prompt, or `upload --resume`).
- Network-loss handling in the app: uploads pause and resume when the connection returns.
- Watch folders and Start with Windows (minimized to the tray).
- Upload queue: files dropped during an upload start when it finishes.
- Upload speed and disk-read graphs, thumbnails in wide windows, resizable window.
- Debug mode with an event log, state, config and system views.

### Changed

- Live Photo repair no longer pairs by file name alone. A pair is offered only when the photo is an HEIC/HEIF camera original (preferred over a JPEG or PNG with the same name) and was taken within a few seconds of the video. Names with only a JPEG or PNG, or with photo and video from different moments, are skipped and listed with the reason. Shared, partner and duplicate entries are ignored.
- Live Photo repair stops when the connection is lost instead of failing every remaining pair; pairs not attempted are reported as skipped and a new scan picks them up. A repair cancelled mid-transfer is reported as skipped, since nothing in the library changed.
- Redesigned interface: app bar, grouped settings, results card with a per-file list.
- Upload progress counts skipped and failed files consistently, so percentage and byte totals agree.
- Sizes are shown in binary units, like Google's storage page.

### Fixed

- Library sync stopped after the first pages; it now reads the whole library.
- Files listed more than once in the library were counted once per copy in quota totals.
- Saving statistics crashed when no statistics file existed yet.
- A stalled library request could hang a scan; each page now times out after two minutes.
- A stalled Live Photo repair could hang forever: a download or upload that moves no data for two minutes is abandoned, and the server must start answering each request within two minutes.
- Live Photo repair failed with "no download URL" when the server returned the video's link outside the one known photo slot. Every container under the response's media section is now searched, and if no link is found the error lists the response's text fields so the layout can be added.
- Live Photo repair sent the photo's capture time to Google as seconds when the library reports milliseconds, a date tens of thousands of years ahead. Times are now read as milliseconds, and a photo without a capture time is skipped instead of linked.
- Videos left in the temp folder by an interrupted Live Photo repair are removed on the next repair (once they are six hours old).
