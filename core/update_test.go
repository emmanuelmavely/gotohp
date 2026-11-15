package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.0", "2.0.0", 0},
		{"v2.1.0", "2.0.0", 1},
		{"2.0.0", "v2.0.1", -1},
		{"10.0.0", "9.9.9", 1},
		{"2.1.0-rc1", "2.1.0", 0},
		{"2.0.0", "dev", 1},
		{"", "2.0.0", -1},
		{"1.2.3.4", "1.0.0", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func withUpdateServer(t *testing.T, handler http.HandlerFunc) {
	server := httptest.NewServer(handler)
	old := updateAPIBase
	updateAPIBase = server.URL
	t.Cleanup(func() {
		updateAPIBase = old
		server.Close()
	})
}

func TestCheckForUpdateNewer(t *testing.T) {
	withUpdateServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+UpdateRepo+"/releases/latest" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent")
		}
		_, _ = w.Write([]byte(`{"tag_name":"v2.1.0","name":"gotohp 2.1.0","body":"- Bin view","html_url":"https://github.com/x/releases/v2.1.0",
			"published_at":"2026-11-15T10:00:00Z","assets":[{"name":"gotohp-x64.exe","browser_download_url":"https://x/a.exe","size":42}]}`))
	})
	info, err := CheckForUpdate(context.Background(), nil, "v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Newer || info.Latest != "2.1.0" || info.Current != "2.0.0" || info.Notes != "- Bin view" {
		t.Fatalf("info = %+v", info)
	}
	if len(info.Assets) != 1 || info.Assets[0].Size != 42 || info.PublishedAt == 0 {
		t.Fatalf("assets/published = %+v", info)
	}
}

func TestCheckForUpdateSameAndMissing(t *testing.T) {
	withUpdateServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v2.0","html_url":"u"}`))
	})
	info, err := CheckForUpdate(context.Background(), nil, "2.0.0")
	if err != nil || info.Newer {
		t.Fatalf("same version: info = %+v, err = %v", info, err)
	}

	withUpdateServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	info, err = CheckForUpdate(context.Background(), nil, "2.0.0")
	if err != nil || info.Latest != "" || info.Newer {
		t.Fatalf("no releases: info = %+v, err = %v", info, err)
	}

	withUpdateServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	if _, err = CheckForUpdate(context.Background(), nil, "2.0.0"); err == nil {
		t.Fatal("rate limit: want an error")
	}
}
