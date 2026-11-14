package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDownloadToFileReportsProgress(t *testing.T) {
	body := strings.Repeat("v", 64*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	api := &Api{client: server.Client()}
	dest := filepath.Join(t.TempDir(), "video.mov")
	var lastDone, lastTotal int64
	if err := api.downloadToFile(context.Background(), server.URL, dest, func(done, total int64) {
		lastDone, lastTotal = done, total
	}); err != nil {
		t.Fatal(err)
	}
	if lastDone != int64(len(body)) || lastTotal != int64(len(body)) {
		t.Fatalf("final progress = %d/%d, want %d/%d", lastDone, lastTotal, len(body), len(body))
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != body {
		t.Fatalf("downloaded file differs (err %v)", err)
	}
}

func TestDownloadToFileStallIsAbandoned(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-release // never sends the rest
	}))
	defer server.Close()
	defer close(release)

	api := &Api{client: server.Client()}
	ctx, kick, stop := stallWatchdog(context.Background(), 100*time.Millisecond)
	defer stop()
	started := time.Now()
	err := api.downloadToFile(ctx, server.URL, filepath.Join(t.TempDir(), "video.mov"), func(int64, int64) { kick() })
	err = transferError(ctx, err)
	if err != errTransferStalled {
		t.Fatalf("err = %v, want errTransferStalled", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("the stalled download was not abandoned promptly")
	}
}

func TestDownloadURLsFromResponse(t *testing.T) {
	photo := encodePBTree([]pbNode{
		nMsg(1,
			nStr(2, "some-id"),
			nMsg(5,
				nMsg(2,
					nStr(5, "https://edited.example/photo"),
					nStr(6, "https://original.example/photo"),
				),
			),
		),
	})
	urls, _, err := downloadURLsFromResponse(photo)
	if err != nil || urls.OriginalURL != "https://original.example/photo" || urls.EditedURL != "https://edited.example/photo" {
		t.Fatalf("photo layout: %+v, %v", urls, err)
	}

	// Videos keep their URLs in another 1.5.N container.
	video := encodePBTree([]pbNode{
		nMsg(1, nMsg(5, nMsg(3, nVarint(1, 7), nStr(6, "https://original.example/video")))),
	})
	urls, _, err = downloadURLsFromResponse(video)
	if err != nil || urls.OriginalURL != "https://original.example/video" || urls.EditedURL != "" {
		t.Fatalf("video layout: %+v, %v", urls, err)
	}

	// An unknown layout falls back to the first URL in the response.
	unknown := encodePBTree([]pbNode{
		nMsg(1, nMsg(9, nMsg(4, nStr(1, "https://somewhere.example/file")))),
	})
	urls, _, err = downloadURLsFromResponse(unknown)
	if err != nil || urls.OriginalURL != "https://somewhere.example/file" {
		t.Fatalf("fallback: %+v, %v", urls, err)
	}

	// No URL at all: the text field paths are returned for the error message.
	none := encodePBTree([]pbNode{nMsg(1, nStr(3, "no url here"), nVarint(4, 1))})
	urls, fields, err := downloadURLsFromResponse(none)
	if err != nil || urls.OriginalURL != "" || urls.EditedURL != "" {
		t.Fatalf("no url: %+v, %v", urls, err)
	}
	if describeFieldPaths(fields) != "1.3" {
		t.Fatalf("fields = %v", fields)
	}

	if _, _, err := downloadURLsFromResponse([]byte{0xff, 0xff, 0xff}); err == nil {
		t.Fatal("malformed input must be reported")
	}
}
