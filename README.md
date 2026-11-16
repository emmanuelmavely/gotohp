# gotohp

![demo](readme_assets/app_demo.webp)

Unofficial Google Photos Desktop GUI Client

- Unlimited uploads (can be disabled)
- Sidebar layout with Upload, Storage, Bin, Live Photos, Activity and Settings pages
- Drag-and-drop file upload interface, with drops queued while an upload runs
- Upload into an existing album picked from a searchable list of your albums (recently used first), or create a new one. If the album cannot be reached, the files stay uploaded and you can pick another album
- Pause and resume uploads, and optional upload/download speed limits (Settings → Network, or `upload --limit-rate 2M`)
- Credential management
- Real-time upload progress: speed and disk-read graphs, time remaining, per-thread status, thumbnails in wide windows
- Configurable upload threads
- Individual files or directories uploads, with optional recursive scanning
- Skips files already present in your account; re-dropped files are not hashed again
- Resumes an interrupted upload (app closed, crash, shutdown) and pauses on network loss until the connection returns
- Watch folders: new photos and videos upload automatically, each folder optionally into its own album, optionally starting with Windows
- Runs in the system tray: closing the window asks whether to keep running there or quit
- Activity: all-time upload totals, a daily chart, the last 30 uploads, and failed files with their reasons and a Retry button
- Live Photo repair: rejoins photos and videos that were uploaded as separate items. A pair is only offered when the photo is an HEIC original and was taken at the same moment as the video; JPEG-only names (often edits or shared copies) are skipped. Repairs run one pair at a time with live progress; it can be cancelled, and stops if the connection drops (scan again to continue). Photos that already have their motion, and pairs already repaired, are not offered again
- Storage: see what counts against your storage and convert it to unlimited with one button, with Pause/Resume and a live graph. Each file is downloaded to a backup folder, its counted copy deleted for good and the download uploaded again without counting (albums, favourites, edits and sharing on it are lost)
- Bin: what is in your Google Photos bin and how many days are left, with Restore, Delete and Empty bin
- Finds album names used by more than one album (Settings → Albums)
- Album takeaway: groups an account's photos into shared albums (Settings → Albums) so they can be shared and saved into another account by hand — useful for consolidating accounts
- Shows the account's Google photo in the sidebar
- About section with the version and a bundled changelog, and a daily check for new releases on GitHub (Settings → About; can be turned off)
- Open uploaded and repaired items in Google Photos with one click (opens in the Google account your browser is signed in to)
- Debug mode with a live event log (Settings → Advanced, or Ctrl/Cmd + Shift + D)
- CLI mode
- Configurable, persistent upload settings (stored in "%system config path%/gotohp/gotohp.config")  
   You can force local config by creating empty gotohp.config next to executable.  
   App data (hash cache, library index, upload journal, upload history, statistics, Live Photo repair record, account photos) is kept in the same folder.

## [Download](https://github.com/emmanuelmavely/gotohp/releases/latest)

Nix users on Linux, see [nix/README.md](nix/README.md).

## CLI usage

Download the `gotohp-cli` release artifact for your platform. It runs without the GUI.

```shell
gotohp-cli upload /path/to/photos --recursive --threads 5
gotohp-cli creds add "<oauth_token cookie value>"   # Embedded Setup sign-in, see below
gotohp-cli creds add "androidId=..."                 # or a raw credential string
gotohp-cli creds add - < token.txt                   # or read either from stdin
gotohp-cli creds set user@gmail.com
```

The CLI shares the config file with the GUI, but reads only the stored credentials from it. Everything else (proxy, quality, upload behaviour) is set by flags, so a scripted run is unaffected by preferences changed in the GUI. Pass `--config <path>` to use a different config file and `--account <email>` to upload with a specific stored account.

More commands:

```shell
gotohp-cli upload /path/to/photos --dry-run          # report what is already uploaded, upload nothing
gotohp-cli upload --resume                           # continue an interrupted run from its journal
gotohp-cli upload /path/to/photos --limit-rate 2M    # cap the total upload speed at 2 MB/s
gotohp-cli quota list                                # files that use storage quota, each file counted once
gotohp-cli quota list --breakdown                    # totals by owner, duplicate, trash and quality
gotohp-cli repair live-photos                        # list Live Photos uploaded as separate items
gotohp-cli repair live-photos --execute              # rejoin them (changes your library)
```

`quota list`, `repair live-photos` and `upload --dry-run` accept `--json`. The first library scan reads the whole library and can take several minutes; later scans only fetch changes.

Run `gotohp-cli help` or `gotohp-cli upload --help` for all commands and options.

The GUI binary runs the same CLI when given a command, e.g. `gotohp upload ...`.

## Sign in

### Option 1 - Embedded setup

1. Open gotohp and click **Add Google account**.
2. Click **Open sign-in** and sign in with the account you want to add.
3. Sign in on the Google Embedded Setup page and click **I agree**. The page may keep loading forever; this is expected.
4. Open the browser developer tools. Under Application or Storage, open Cookies for `accounts.google.com`.
5. Copy only the value of the `oauth_token` cookie and paste it into gotohp.
6. Click **Connect account**.

From the CLI, open the same page yourself and run `gotohp-cli creds add "<oauth_token value>"`.

### Option 2 - ReVanced. No root required

<details>
  <summary><strong>Click to expand</strong></summary>

1. Install Google Photos ReVanced on your android device/emulator.
   - Install GmsCore [https://github.com/ReVanced/GmsCore/releases](https://github.com/ReVanced/GmsCore/releases)
   - Install patched apk [https://github.com/RookieEnough/Morphe-AutoBuilds/releases](https://github.com/RookieEnough/Morphe-AutoBuilds/releases) or patch it yourself
2. Connect the device to your PC via ADB.
3. Open the terminal on your PC and execute

   Windows

   ```cmd
   adb logcat | FINDSTR "auth%2Fphotos.native"
   ```

   Linux/Mac

   ```shell
   adb logcat | grep "auth%2Fphotos.native"
   ```

4. If you are already using ReVanced - remove Google Account from GmsCore.
5. Open Google Photos ReVanced on your device and log into your account.
6. One or more identical GmsCore logs should appear in the terminal.
7. Copy text from `androidId=` to the end of the line from any log.
8. That's it! 🎉

</details>

### Option 3 - Official apk. Root required

<details>
  <summary><strong>Click to expand</strong></summary>

1. Get a rooted android device or an emulator.
2. Connect the device to your PC via ADB.
3. Install [HTTP Toolkit](https://httptoolkit.com)
4. In HTTP Toolkit, select Intercept - `Android Device via ADB`. Filter traffic with

   ```text
   contains(https://www.googleapis.com/auth/photos.native)
   ```

   Or if you have an older version of Google Photos, try

   ```text
   contains(www.googleapis.com%2Fauth%2Fplus.photos.readwrite)
   ```

5. Open Google Photos app and login with your account.
6. A single request should appear.  
   Copy request body as text.
7. Add that credential string in gotohp.
8. If gotohp asks for a token binding key, keep the rooted device connected and click `Read from ADB`. gotohp will read the account's `lstBindingKeyAlias` from Android AccountManager and save it into the credential.

#### Troubleshooting

- **No Auth Request Intercepted**
  1. Log out of your Google account.
  2. Log in again.
  3. Try `Android App via Frida` interception method in HTTP Toolkit.
- **Token binding key not found**
  1. Make sure the same Google account is present on the connected device.
  2. Make sure root is available to ADB.

</details>

## Build

Follow official wails3 guide
[https://v3.wails.io/getting-started/installation/](https://v3.wails.io/getting-started/installation/)

### Release

Push a tag like `v2.1.0` (or merge the Release Please pull request). The "Build Release" workflow sets the app version from the tag, records the commit and build date, builds every platform and attaches the files to the GitHub release. The app's update check reads that release. Before tagging, rename "Unreleased" in CHANGELOG.md to the version, since the changelog is bundled with the app.
