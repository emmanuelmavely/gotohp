package core

import (
	"context"
	"encoding/hex"
	"fmt"
)

// DryRunEntry is one classified work item of a dry run.
type DryRunEntry struct {
	// Path is the primary path; a Live Photo entry covers both components.
	Path    string
	Kind    string
	// Size is the combined size of the item's components.
	Size    int64
	// Uploaded reports whether every component's content already exists
	// remotely (and is not in the trash).
	Uploaded bool
}

// DryRunReport compares a local selection against the remote library.
type DryRunReport struct {
	Entries []DryRunEntry
	// UploadedCount and UploadedBytes cover what would be skipped as
	// already uploaded.
	UploadedCount int
	UploadedBytes int64
	// UploadCount and UploadBytes cover what would actually be uploaded;
	// UploadBytes is also the estimated quota impact (saver quality may be
	// charged less).
	UploadCount int
	UploadBytes int64
	// SkippedCount is the number of preflight-skipped items, e.g.
	// incomplete Live Photo pairs.
	SkippedCount int
}

// BuildDryRunReport classifies the paths exactly like an upload run would
// (file filter, Live Photo pairing, preflight skips), hashes every component
// through hashCache, and marks an item uploaded when its content is already
// in the library according to uploadedHashes. opts.ForceUpload counts
// everything as to-upload.
func BuildDryRunReport(ctx context.Context, paths []string, opts UploadOptions, hashCache *HashCache, uploadedHashes map[string]struct{}) (*DryRunReport, error) {
	opts = opts.normalized()
	targetPaths, err := filterGooglePhotosFilesWithCancel(paths, opts, nil)
	if err != nil {
		return nil, err
	}
	workItems, warnings := ClassifyUploadWork(targetPaths, LivePhotoClassificationOptions{
		Enabled:             opts.PairLivePhotos,
		SkipIncomplete:      opts.SkipIncompleteLivePhotos,
		IgnoreAppleMetadata: opts.IgnoreAppleMetadata,
	}, nil)

	report := &DryRunReport{}
	for _, warning := range warnings {
		if isSkippedPreflightWarning(warning.Code) {
			report.SkippedCount++
		}
	}

	for _, item := range workItems {
		entry := DryRunEntry{
			Path: uploadWorkPrimaryPath(item),
			Kind: string(item.Kind),
			Size: sumPathSizes(uploadWorkPaths(item)),
		}
		entry.Uploaded = !opts.ForceUpload
		for _, path := range uploadWorkPaths(item) {
			sha, err := calculateSHA1Cached(ctx, path, hashCache)
			if err != nil {
				return nil, fmt.Errorf("hash %s: %w", path, err)
			}
			if _, uploaded := uploadedHashes[hex.EncodeToString(sha)]; !uploaded {
				entry.Uploaded = false
				break
			}
		}
		report.Entries = append(report.Entries, entry)
		if entry.Uploaded {
			report.UploadedCount++
			report.UploadedBytes += entry.Size
		} else {
			report.UploadCount++
			report.UploadBytes += entry.Size
		}
	}
	return report, nil
}
