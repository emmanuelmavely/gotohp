package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// UpdateRepo is the GitHub repository whose releases the update check reads.
const UpdateRepo = "emmanuelmavely/gotohp"

// updateAPIBase is the GitHub API root; tests point it at a local server.
var updateAPIBase = "https://api.github.com"

// ReleaseAsset is one downloadable file of a release.
type ReleaseAsset struct {
	Name string
	URL  string
	Size int64
}

// UpdateInfo is the result of an update check.
type UpdateInfo struct {
	// Current is the running version, Latest the newest release's version
	// (both without a leading "v"). Latest is empty when the repository has
	// no releases yet.
	Current string
	Latest  string
	// Newer is true when Latest is a higher version than Current.
	Newer bool
	// Name and Notes are the release title and its notes (Markdown).
	Name  string
	Notes string
	// URL is the release page.
	URL string
	// PublishedAt and CheckedAt are Unix milliseconds.
	PublishedAt int64
	CheckedAt   int64
	Assets      []ReleaseAsset
}

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// CheckForUpdate asks GitHub for the latest published release (drafts and
// pre-releases are not counted) and compares it with current. client may be
// nil for a default client.
func CheckForUpdate(ctx context.Context, client *http.Client, current string) (UpdateInfo, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	info := UpdateInfo{Current: strings.TrimPrefix(current, "v"), CheckedAt: time.Now().UnixMilli(), Assets: []ReleaseAsset{}}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/repos/%s/releases/latest", updateAPIBase, UpdateRepo), nil)
	if err != nil {
		return info, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "gotohp/"+info.Current)

	response, err := client.Do(request)
	if err != nil {
		return info, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return info, fmt.Errorf("could not read GitHub's answer: %w", err)
	}

	switch {
	case response.StatusCode == http.StatusNotFound:
		// No published release yet.
		return info, nil
	case response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests:
		return info, fmt.Errorf("GitHub is limiting requests right now; try again later")
	case response.StatusCode != http.StatusOK:
		return info, fmt.Errorf("GitHub answered %s", response.Status)
	}

	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return info, fmt.Errorf("could not read GitHub's answer: %w", err)
	}
	info.Latest = strings.TrimPrefix(release.TagName, "v")
	info.Name = release.Name
	info.Notes = release.Body
	info.URL = release.HTMLURL
	if !release.PublishedAt.IsZero() {
		info.PublishedAt = release.PublishedAt.UnixMilli()
	}
	for _, asset := range release.Assets {
		info.Assets = append(info.Assets, ReleaseAsset{Name: asset.Name, URL: asset.BrowserDownloadURL, Size: asset.Size})
	}
	info.Newer = CompareVersions(info.Latest, info.Current) > 0
	return info, nil
}

// ParseVersion reads "v2", "2.0" or "2.1.3" (a leading "v" and anything after
// "-" or "+" are ignored) into major, minor and patch numbers.
func ParseVersion(version string) ([3]int, bool) {
	var parts [3]int
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if cut := strings.IndexAny(version, "-+"); cut >= 0 {
		version = version[:cut]
	}
	if version == "" {
		return parts, false
	}
	fields := strings.Split(version, ".")
	if len(fields) > 3 {
		return parts, false
	}
	for i, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil || number < 0 {
			return parts, false
		}
		parts[i] = number
	}
	return parts, true
}

// CompareVersions returns 1 when a is newer than b, -1 when older and 0 when
// equal. A version that cannot be read is never newer.
func CompareVersions(a, b string) int {
	left, okLeft := ParseVersion(a)
	right, okRight := ParseVersion(b)
	switch {
	case !okLeft && !okRight:
		return 0
	case !okLeft:
		return -1
	case !okRight:
		return 1
	}
	for i := range left {
		if left[i] != right[i] {
			if left[i] > right[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}
