package core

import (
	"path/filepath"
	"testing"
)

func TestWatchAlbumSettings(t *testing.T) {
	folder := filepath.Join("C:", "Pictures", "Screenshots")
	entries := setWatchAlbum(nil, folder, "Screens")
	entries = setWatchAlbum(entries, filepath.Join("C:", "Other"), "AF1QipKey")
	entries = setWatchAlbum(entries, folder, "Screens 2")
	albums := ParseWatchAlbums(entries)
	if len(albums) != 2 || albums[filepath.Clean(folder)] != "Screens 2" {
		t.Fatalf("albums = %v", albums)
	}
	entries = setWatchAlbum(entries, folder, "")
	if albums := ParseWatchAlbums(entries); len(albums) != 1 {
		t.Fatalf("clearing an album left %v", albums)
	}
	if albums := ParseWatchAlbums([]string{"no separator", "|empty folder", "folder|"}); len(albums) != 0 {
		t.Fatalf("bad entries parsed as %v", albums)
	}
}

func TestWatchUploadGroups(t *testing.T) {
	root := filepath.Join("/", "watch")
	nested := filepath.Join(root, "phone")
	folders := []string{root, nested, filepath.Join("/", "plain")}
	albums := map[string]string{nested: "Phone"}
	paths := []string{
		filepath.Join(root, "a.jpg"),
		filepath.Join(nested, "b.jpg"),
		filepath.Join(nested, "deeper", "c.jpg"),
		filepath.Join("/", "plain", "d.jpg"),
		filepath.Join("/", "watchlater", "e.jpg"),
	}
	groups := WatchUploadGroups(paths, folders, albums)
	if len(groups["Phone"]) != 2 || len(groups[""]) != 3 {
		t.Fatalf("groups = %v", groups)
	}
	if WatchFolderOf(filepath.Join("/", "watchlater", "e.jpg"), folders) != "" {
		t.Fatal("a sibling folder with a shared prefix matched")
	}
}
