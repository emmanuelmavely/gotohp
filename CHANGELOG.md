# Changelog

Notable changes in this fork. The base is gotohp 0.11.0.

## 2.1.0 (2026-11-15)

### Added

- About section in Settings: the version, the commit and date of the build, and "What's new" with this changelog, which is bundled with the app.
- Update check: on start, at most once a day, the app asks GitHub for the latest release of `emmanuelmavely/gotohp`. A newer version shows a banner with its release notes and a Download button that opens the release page. "Check now" in Settings checks right away; the automatic check can be turned off. Nothing is downloaded or installed by the app.
- Close to tray: closing the window asks whether to minimize to the tray or quit, with a "don't ask again" option and a setting to change it later. Clicking the tray icon reopens the window; the tray menu offers Open and Quit.
- Live library scan: while the storage count or the Live Photo scan runs, the app shows the running total, items read, scan speed and, after a first full scan, an estimated percentage and time left.
- Statistics sheet: storage the account really uses (each file counted once), all-time upload and download totals, average speed and a 30-day upload history.
- Storage strip under the account name with a one-click storage count. The last count is remembered per account between sessions and shows how long ago it was made; Refresh counts again.
- Live Photo repair in the app and as `repair live-photos`: finds photos whose video was uploaded as a separate item and rejoins them after verifying the video.
- Live Photo repair progress: each pair shows its stage (download, verify, upload, link, trash copy) with transfer progress and speed, plus overall progress and running counts. A repair can be cancelled and keeps running while the statistics sheet is closed. `repair live-photos --execute` prints a live line and each result as it happens.
- "Open in Google Photos" links on Live Photo repair results, repair candidates and uploaded files. They open in the system browser, for the Google account it is signed in to.
- Bin view in Statistics: lists what is in the Google Photos bin with when each item was deleted and how many days are left before Google removes it, and marks the videos the Live Photo repair put there (with a link to the photo they now belong to). Filter by "From repair" or "Other". Read-only.
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

- Release builds take their version from the pushed tag (`v2.1` becomes 2.1.0) and record the commit and build date, so the window title, the exe's file properties, `gotohp version` and the update check all agree. The version files now start from 2.0.0, matching the published v2.0 release.
- Live Photo repair no longer pairs by file name alone. A pair is offered only when the photo is an HEIC/HEIF camera original (preferred over a JPEG or PNG with the same name) and was taken within a few seconds of the video. Names with only a JPEG or PNG, or with photo and video from different moments, are skipped and listed with the reason. Shared, partner and duplicate entries are ignored.
- Live Photo repair remembers every pair it repaired (`live-photo-repairs.json` next to the config), so a scan no longer offers them again when Google keeps listing the joined motion part as its own video. Pairs repaired before this record existed are recognised when a copy of their video is already in the bin. Both are listed as "already a Live Photo", and in `repair live-photos` output and JSON (`alreadyRepaired`).
- Live Photo repair stops when the connection is lost instead of failing every remaining pair; pairs not attempted are reported as skipped and a new scan picks them up. A repair cancelled mid-transfer is reported as skipped, since nothing in the library changed.
- Redesigned interface: app bar, grouped settings, results card with a per-file list.
- Upload progress counts skipped and failed files consistently, so percentage and byte totals agree.
- Sizes are shown in binary units, like Google's storage page.

### Fixed

- Live Photo repair offered photos that were already Live Photos whenever a separate copy of their video was also in the library. The library scan now reads whether a photo already has its motion part (as Google reports it), lists those pairs as "already a Live Photo" and never repairs them. The library index format changed for this, so the first scan after updating reads the whole library again.
- Library sync stopped after the first pages; it now reads the whole library.
- Files listed more than once in the library were counted once per copy in quota totals.
- Saving statistics crashed when no statistics file existed yet.
- A stalled library request could hang a scan; each page now times out after two minutes.
- A stalled Live Photo repair could hang forever: a download or upload that moves no data for two minutes is abandoned, and the server must start answering each request within two minutes.
- Live Photo repair failed with "no download URL" when the server returned the video's link outside the one known photo slot. Every container under the response's media section is now searched, and if no link is found the error lists the response's text fields so the layout can be added.
- Live Photo repair sent the photo's capture time to Google as seconds when the library reports milliseconds, a date tens of thousands of years ahead. Times are now read as milliseconds, and a photo without a capture time is skipped instead of linked.
- Videos left in the temp folder by an interrupted Live Photo repair are removed on the next repair (once they are six hours old).
