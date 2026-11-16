# Changelog

Notable changes in this fork. The base is gotohp 0.11.0.

## 2.2.0 (2026-11-17)

### Added

- Album takeaway (Settings → Albums): groups every photo and video in the selected account into shared albums named "gotohp takeaway 1, 2, …", a chosen number of items each (default 2,000; smaller albums are quicker to save on a phone). It only groups — nothing is uploaded, moved or deleted — so you can open each album in Google Photos, share it, and Save it into another account by hand, then delete the albums. Shared, partner and trashed items, and duplicates of the same stored file, are left out. Check shows how many items and albums it would make before you start; the run shows progress and can be stopped, keeping the albums already made, and lists each album with a link to open it in Google Photos.
- New layout with a sidebar: Upload, Storage, Bin, Live Photos, Activity and Settings each have their own page instead of sheets. A dot in the sidebar shows what is running (an upload, a storage conversion, a Live Photo repair). In a narrow window the sidebar shows icons only.
- Storage page: shows how much counts against your storage (read once, then kept; Refresh reads it again), how many files were converted earlier with a Check, and one Convert button behind a short warning and a confirmation tick. While it runs: percentage, space freed, download speed, elapsed and remaining time, a download/upload graph, and Pause. Pause finishes the batch of 20 in flight; Resume continues from there. "Try 10 files first" stays as a small link.
- Bin page: item count and size, Restore, Delete and Empty bin, with a confirmation before anything is deleted for good. Items whose content is still in the library (deleting them could take the library copy too) and items from shared albums are left alone and listed in the result.
- Activity page: Overview (the all-time totals and 30-day chart), History (the last 30 uploads with time, counts, size and album) and Problems (failed files with the reason, Retry all and Dismiss, and uploads whose album could not be reached). The history is kept in `upload-history.json` next to the config.
- Each watch folder can have its own album (Settings → Watch folders), chosen from your existing albums. Folders without one upload without an album.
- Settings → Albums lists album names that are used by more than one album, with links to open each one in Google Photos to merge them there (the app has no way to move photos between albums).
- The album picker lists the albums you used recently first.
- The selected account's photo is shown next to the account in the sidebar. It is fetched from Google once a day and kept in `avatars/` next to the config; the account's initial is shown until then or without a connection.

- Album picker: dropping files on the Album zone lists your existing albums (searchable, with item counts) so new uploads can go into an album you already have instead of creating a duplicate; typing a name that matches no album creates a new one. The albums are fetched in the background when the app starts, so the list is ready when you drop files; the very first fetch reads the whole library once (a few minutes), after that only changes are fetched. Library reads now run one at a time.
- Make counted storage unlimited (Statistics): one button checks how much counts, then "Convert all" (or "Try 10 first") downloads each counted file into a backup folder, deletes the counted copy for good and uploads the download again without counting, 20 at a time, and continues where it stopped when run again. Originals are checked against the library's fingerprint; storage-saver files have no original in Google Photos, so their stored copy is uploaded again as is. Live Photos are left alone. The step-by-step tools below moved under "Advanced".
- Free up counted storage, advanced (Statistics): finds the files that count against your storage and have a byte-identical copy in folders you choose, shows what each folder would free, and converts the ones you select. Each counted copy is deleted from Google Photos for good after its local copy is checked again, and the local copy is uploaded again without counting, 20 files at a time. Albums, favourites, archive status, descriptions, edits made in Google Photos and sharing on those files are lost; the panel says so before anything can be converted and flags favourites and edited files. Live Photos and files listed more than once are left alone. A record (`quota-conversion.json`) keeps track of every converted file, so "Check status" shows which are back without counting and "Upload waiting files" finishes any that were deleted but not uploaded yet. The search shows its two steps (reading the online library, then searching the folders) and explains counted files that exist locally only as a different version (same name, different content). Counted files with no copy on this computer can be downloaded first as their exact originals (checked against the library's SHA-1, saved one folder per year with the capture date as file date, and kept as a backup), then converted the same way. Storage-saver files and Live Photos are not offered for download.
- The library scan now also reads whether a file is a favourite, archived or edited in Google Photos. The index format changed again, so the next scan reads the whole library.
- Pause and Resume for uploads. Pause stops the upload and keeps a record of what is left; Resume continues with the files not uploaded yet (files that were in the middle of uploading start again). Files dropped while paused wait in the queue, and watch folders wait too.
- Speed limits in Settings → Network: one for uploads (shared by all upload threads) and one for downloads (used by the Live Photo repair), in MB/s. They apply right away, also to a transfer already running. `upload --limit-rate 2M` sets the upload limit in the CLI.

### Changed

- Notifications appear at the bottom right, have a close button and always close by themselves. Failed files share one notification that counts up, with a Details button that opens Activity → Problems.
- The storage strip under the account name and the Statistics and Settings sheets are replaced by the sidebar pages. The step-by-step storage tools ("Advanced") are no longer shown in the app; the CLI `quota` commands are unchanged.
- "Retry now" on a paused upload continues the paused run the same way Resume does.
- Settings and Statistics can be opened while an upload runs. Settings changes apply to the next upload; speed limits apply at once. A Live Photo repair waits until the upload is done or paused.

### Fixed

- Watch folder uploads used the album picked for the last dropped batch, so every batch of new files created another album with that name. Watched files now go only to their folder's own album, or none.
- When the album of a dropped batch cannot be reached (for example it was deleted), the files stay uploaded, the album list is read again, and a notification offers "Pick an album" to put those files into another album or a new one, or "Leave out".
- Pausing or cancelling reported the files that were uploading at that moment as failed ("context canceled"). They are now treated as not uploaded yet: no error, and Resume uploads them.

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
