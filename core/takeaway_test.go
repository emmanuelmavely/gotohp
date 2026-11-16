package core

import (
	"context"
	"testing"
)

func TestTakeawayMediaKeys(t *testing.T) {
	items := []*RemoteMediaItem{
		{MediaKey: "a", DedupKey: "da", Origin: OriginSelf},
		{MediaKey: "b", DedupKey: "db"}, // origin 0 is treated as own
		{MediaKey: "c", DedupKey: "dc", Trashed: true},
		{MediaKey: "d", DedupKey: "dd", Origin: OriginShared},
		{MediaKey: "e", DedupKey: "de", Origin: OriginPartner},
		{MediaKey: "a2", DedupKey: "da", Origin: OriginSelf}, // same stored file as a
		{MediaKey: "", DedupKey: "dz"},                       // no key
		{MediaKey: "f", DedupKey: "", Origin: OriginSelf},    // no dedup: kept
	}
	keys, skipped := TakeawayMediaKeys(items)
	if got := joinKeys(keys); got != "a,b,f" {
		t.Fatalf("keys = %q", got)
	}
	// trashed, shared, partner, and the duplicate of a.
	if skipped != 4 {
		t.Fatalf("skipped = %d", skipped)
	}
}

func TestNormalizeTakeawayAlbumSize(t *testing.T) {
	for in, want := range map[int]int{0: DefaultTakeawayAlbumSize, -5: DefaultTakeawayAlbumSize, 500: 500, 99999: AlbumLimit} {
		if got := NormalizeTakeawayAlbumSize(in); got != want {
			t.Errorf("NormalizeTakeawayAlbumSize(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestPlanTakeaway(t *testing.T) {
	items := make([]*RemoteMediaItem, 2050)
	for i := range items {
		items[i] = &RemoteMediaItem{MediaKey: itoa(i), DedupKey: itoa(i), Origin: OriginSelf}
	}
	items = append(items, &RemoteMediaItem{MediaKey: "shared", DedupKey: "s", Origin: OriginShared})
	plan := PlanTakeaway(items, 2000)
	if plan.Items != 2050 || plan.Albums != 2 || plan.Skipped != 1 {
		t.Fatalf("plan = %+v", plan)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func joinKeys(keys []string) string {
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += k
	}
	return out
}

func TestGatherTakeawayStops(t *testing.T) {
	// A cancelled context stops before the first request, with no albums.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var api *Api
	result, err := api.GatherTakeaway(ctx, []string{"a", "b"}, 1, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !result.Stopped || len(result.Albums) != 0 {
		t.Fatalf("result = %+v", result)
	}
}
