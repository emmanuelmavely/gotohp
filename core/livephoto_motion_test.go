package core

import "testing"

func TestHasProtoPathFindsMicroVideo(t *testing.T) {
	// Item path 5 as the library sync sends it for a Live Photo still: the
	// type, then micro video data at 5.2.4 inside it.
	livePhoto := encodePBTree([]pbNode{
		nVarint(1, MediaTypePhoto),
		nMsg(2, nMsg(1, nStr(1, "https://lh3/x"))),
		nMsg(5, nVarint(1, 3), nMsg(2, nVarint(1, 9), nMsg(4, nVarint(1, 2500), nVarint(4, 1440), nVarint(5, 1920)))),
	})
	if !hasProtoPath(livePhoto, 5, 2, 4) {
		t.Fatal("micro video not found")
	}
	plainPhoto := encodePBTree([]pbNode{
		nVarint(1, MediaTypePhoto),
		nMsg(5, nVarint(1, 3), nMsg(2, nVarint(1, 9))),
	})
	if hasProtoPath(plainPhoto, 5, 2, 4) {
		t.Fatal("plain photo reported as having motion")
	}
	if hasProtoPath([]byte{0xff, 0xff}, 5, 2, 4) {
		t.Fatal("garbage reported as having motion")
	}
}

func TestPhotoWithMotionIsNotOffered(t *testing.T) {
	const shot = int64(1709461164526)
	const clip = int64(1709461163000)
	items := []*RemoteMediaItem{
		{MediaKey: "P1", FileName: "IMG_0003.HEIC", Type: MediaTypePhoto, DedupKey: "PD1", IsCanonical: true, UTCTimestamp: shot, HasMotion: true},
		{MediaKey: "V1", FileName: "IMG_0003.MOV", Type: MediaTypeVideo, DedupKey: "VD1", IsCanonical: true, UTCTimestamp: clip},
		{MediaKey: "P2", FileName: "IMG_0004.HEIC", Type: MediaTypePhoto, DedupKey: "PD2", IsCanonical: true, UTCTimestamp: shot},
		{MediaKey: "V2", FileName: "IMG_0004.MOV", Type: MediaTypeVideo, DedupKey: "VD2", IsCanonical: true, UTCTimestamp: clip},
	}
	report := findLivePhotoRepairReport(items, LivePhotoRepairLedger{})
	if len(report.Candidates) != 1 || report.Candidates[0].Stem != "img_0004" {
		t.Fatalf("candidates = %+v", report.Candidates)
	}
	if len(report.AlreadyRepaired) != 1 || report.AlreadyRepaired[0].PhotoMediaKey != "P1" {
		t.Fatalf("already repaired = %+v", report.AlreadyRepaired)
	}
}
