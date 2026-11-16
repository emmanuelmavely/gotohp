package core

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSizedAccountPhotoURL(t *testing.T) {
	base := "https://lh3.googleusercontent.com/a-/ALV-Uj"
	for in, want := range map[string]string{
		base + "=s96-c": base + "=s48-c",
		base + "=s96":   base + "=s48-c",
		base:            base + "=s48-c",
	} {
		if got := SizedAccountPhotoURL(in, 48); got != want {
			t.Errorf("SizedAccountPhotoURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAccountPhotoPath(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")
	path := AccountPhotoPath(" Some.One+x@Gmail.com ")
	if filepath.Base(path) != "some.one+x@gmail.com.jpg" || filepath.Base(filepath.Dir(path)) != "avatars" {
		t.Fatalf("path = %q", path)
	}
	if strings.ContainsAny(filepath.Base(AccountPhotoPath(`a/b\c:d@x`)), `/\:`) {
		t.Fatal("unsafe characters kept")
	}
}

func TestIsAlbumNotFound(t *testing.T) {
	for _, text := range []string{"server returned status 404", "NOT_FOUND: album", "album not found"} {
		if !IsAlbumNotFound(errors.New(text)) {
			t.Errorf("%q not recognised", text)
		}
	}
	if IsAlbumNotFound(errors.New("status 500")) || IsAlbumNotFound(nil) {
		t.Error("other errors recognised as not found")
	}
}

func TestDuplicateAlbums(t *testing.T) {
	groups := DuplicateAlbums([]RemoteAlbum{
		{MediaKey: "1", Title: "my chuppu", ItemCount: 1},
		{MediaKey: "2", Title: "My Chuppu ", ItemCount: 30},
		{MediaKey: "3", Title: "my chuppu", ItemCount: 1},
		{MediaKey: "4", Title: "Trip", ItemCount: 5},
		{MediaKey: "5", Title: "trip", ItemCount: 2},
		{MediaKey: "6", Title: "Alone"},
		{MediaKey: "7", Title: " "},
	})
	if len(groups) != 2 || len(groups[0].Albums) != 3 || groups[0].Albums[0].MediaKey != "2" || groups[0].Items != 32 {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestBinSelection(t *testing.T) {
	report := BinReport{Items: []BinItem{
		{MediaKey: "a", DedupKey: "da"},
		{MediaKey: "b", DedupKey: "db", CopyInLibrary: true},
		{MediaKey: "c", DedupKey: "dc", Shared: true},
		{MediaKey: "d"},
		{MediaKey: "e", DedupKey: "de"},
	}}
	keys, skipped := BinSelection(report, nil)
	if strings.Join(keys, ",") != "da,de" || len(skipped) != 3 {
		t.Fatalf("all: keys %v skipped %v", keys, skipped)
	}
	keys, skipped = BinSelection(report, []string{"e", "b"})
	if strings.Join(keys, ",") != "de" || len(skipped) != 1 {
		t.Fatalf("chosen: keys %v skipped %v", keys, skipped)
	}
}
