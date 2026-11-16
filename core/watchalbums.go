package core

import (
	"path/filepath"
	"strings"
)

// ParseWatchAlbums reads "folder|album" entries.
func ParseWatchAlbums(entries []string) map[string]string {
	albums := make(map[string]string, len(entries))
	for _, entry := range entries {
		folder, album, ok := strings.Cut(entry, "|")
		if ok && strings.TrimSpace(folder) != "" && strings.TrimSpace(album) != "" {
			albums[filepath.Clean(folder)] = strings.TrimSpace(album)
		}
	}
	return albums
}

func setWatchAlbum(entries []string, folder string, album string) []string {
	folder = filepath.Clean(strings.TrimSpace(folder))
	album = strings.TrimSpace(album)
	kept := make([]string, 0, len(entries)+1)
	for _, entry := range entries {
		existing, _, _ := strings.Cut(entry, "|")
		if !strings.EqualFold(filepath.Clean(existing), folder) {
			kept = append(kept, entry)
		}
	}
	if album != "" && folder != "." {
		kept = append(kept, folder+"|"+album)
	}
	return kept
}

// WatchFolderOf returns the watch folder that contains path, the longest
// match when folders nest, or "" when none does.
func WatchFolderOf(path string, folders []string) string {
	path = filepath.Clean(path)
	best := ""
	for _, folder := range folders {
		folder = filepath.Clean(folder)
		rel, err := filepath.Rel(folder, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		if len(folder) > len(best) {
			best = folder
		}
	}
	return best
}

// WatchUploadGroups splits a batch of watched files by the album their watch
// folder uploads to. Files whose folder has no album share the "" group.
func WatchUploadGroups(paths []string, folders []string, albums map[string]string) map[string][]string {
	groups := map[string][]string{}
	for _, path := range paths {
		album := ""
		if folder := WatchFolderOf(path, folders); folder != "" {
			for configured, name := range albums {
				if strings.EqualFold(filepath.Clean(configured), folder) {
					album = name
					break
				}
			}
		}
		groups[album] = append(groups[album], path)
	}
	return groups
}
