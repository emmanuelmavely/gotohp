package core

import "testing"

func TestParseRemoteAlbum(t *testing.T) {
	raw := encodePBTree([]pbNode{
		nStr(1, "AF1QipAlbum1"),
		nMsg(2,
			nStr(5, "Trip to Iceland"),
			nVarint(7, 42),
			nVarint(8, 1),
			nMsg(10, nVarint(10, 1731600000000)),
			nMsg(17, nStr(1, "AF1QipCover")),
		),
	})
	album := parseRemoteAlbum(raw)
	if album == nil || album.MediaKey != "AF1QipAlbum1" || album.Title != "Trip to Iceland" || album.ItemCount != 42 ||
		album.Type != 1 || album.CoverMediaKey != "AF1QipCover" || album.LastActivity != 1731600000000 {
		t.Fatalf("album = %+v", album)
	}
	if parseRemoteAlbum(encodePBTree([]pbNode{nMsg(2, nStr(5, "no key"))})) != nil {
		t.Fatal("an entry without a key should be skipped")
	}

	// Several collections at 1.3 next to other fields.
	field1 := encodePBTree([]pbNode{nStr(1, "resume"), nMsg(3, nStr(1, "A")), nVarint(4, 9), nMsg(3, nStr(1, "B"))})
	if got := protoRepeatedBytes(field1, 3); len(got) != 2 || protoStringAt(got[1], 1) != "B" {
		t.Fatalf("collections = %q", got)
	}
}

func TestAccumulatorAlbums(t *testing.T) {
	library := newLibraryAccumulator()
	library.addAlbum(&RemoteAlbum{MediaKey: "A", Title: "Old name"})
	library.apply(&LibrarySyncPage{
		Albums:         []*RemoteAlbum{{MediaKey: "A", Title: "New name"}, {MediaKey: "B", Title: "Second"}, {MediaKey: "C"}},
		AlbumDeletions: []string{"C"},
	})
	albums := library.albumList()
	if len(albums) != 2 || albums[0].Title != "New name" || albums[1].MediaKey != "B" {
		t.Fatalf("albums = %+v", albums)
	}
}

func TestAlbumsOf(t *testing.T) {
	if list := AlbumsOf(nil); list.Known || len(list.Albums) != 0 {
		t.Fatalf("nil index = %+v", list)
	}
	index := &LibraryIndex{SyncedAt: 5, AlbumsKnown: true, Albums: []*RemoteAlbum{
		{MediaKey: "A", Title: "Older", LastActivity: 1},
		{MediaKey: "B", Title: " ", LastActivity: 9},
		{MediaKey: "C", Title: "Newer", LastActivity: 7},
	}}
	list := AlbumsOf(index)
	if !list.Known || list.SyncedAt != 5 || len(list.Albums) != 2 || list.Albums[0].MediaKey != "C" {
		t.Fatalf("list = %+v", list)
	}
}
