package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"app/core"

	"github.com/spf13/pflag"
)

// runDryRun implements `upload --dry-run`: it syncs the remote library (using
// the local index cache for the delta) and reports which of the given paths
// are already uploaded, without uploading anything.
func runDryRun(paths []string, opts core.UploadOptions, f *pflag.FlagSet) error {
	api, err := core.NewApi(opts.Api)
	if err != nil {
		return fmt.Errorf("failed to create api client: %w", err)
	}

	cached, _ := core.LoadLibraryIndex()
	verb := "Syncing"
	if cached != nil && cached.SyncToken != "" {
		verb = "Updating"
	}
	live := isTerminal(os.Stderr)
	started := time.Now()
	index, err := api.SyncLibraryIncremental(cached, func(s core.LibrarySyncStats) {
		if live {
			fmt.Fprintf(os.Stderr, "\r%-100s", syncProgressLine(verb, s, time.Since(started)))
		}
	})
	if live {
		fmt.Fprintf(os.Stderr, "\r%-100s\r", "")
	}
	if err != nil {
		return fmt.Errorf("failed to sync remote library: %w", err)
	}
	if err := index.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: could not save the library index: %v\n", err)
	}
	fmt.Fprintln(os.Stderr, syncProgressLine("Synced", core.LibrarySyncStats{
		Items:        len(index.Items),
		ChargedItems: len(core.QuotaChargedMedia(index.Items)),
	}, time.Since(started)))

	// The hash cache makes repeat dry runs of the same folder instant.
	hashCache := core.NewHashCache(core.HashCachePath())
	report, err := core.BuildDryRunReport(context.Background(), paths, opts, hashCache, index.UploadedHashes())
	if err != nil {
		return err
	}
	hashCache.Save()

	if asJSON, _ := f.GetBool("json"); asJSON {
		return printDryRunJSON(report)
	}
	printDryRunSummary(report)
	return nil
}

func printDryRunSummary(report *core.DryRunReport) {
	skipNote := ""
	if report.SkippedCount > 0 {
		skipNote = fmt.Sprintf(", %d skipped by preflight", report.SkippedCount)
	}
	fmt.Printf("dry run: %d item(s) to check%s\n", len(report.Entries), skipNote)
	fmt.Printf("  already uploaded: %d item(s), %s (skipped on a real upload)\n",
		report.UploadedCount, humanBytes(report.UploadedBytes))
	fmt.Printf("  would upload:     %d item(s), %s (estimated quota impact)\n",
		report.UploadCount, humanBytes(report.UploadBytes))
}

type dryRunEntryJSON struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Uploaded bool   `json:"uploaded"`
}

type dryRunReportJSON struct {
	Items         []dryRunEntryJSON `json:"items"`
	UploadedCount int               `json:"uploadedCount"`
	UploadedBytes int64             `json:"uploadedBytes"`
	UploadCount   int               `json:"uploadCount"`
	UploadBytes   int64             `json:"uploadBytes"`
	SkippedCount  int               `json:"skippedCount"`
}

func printDryRunJSON(report *core.DryRunReport) error {
	out := dryRunReportJSON{
		Items:         make([]dryRunEntryJSON, 0, len(report.Entries)),
		UploadedCount: report.UploadedCount,
		UploadedBytes: report.UploadedBytes,
		UploadCount:   report.UploadCount,
		UploadBytes:   report.UploadBytes,
		SkippedCount:  report.SkippedCount,
	}
	for _, entry := range report.Entries {
		out.Items = append(out.Items, dryRunEntryJSON{
			Path:     entry.Path,
			Kind:     entry.Kind,
			Size:     entry.Size,
			Uploaded: entry.Uploaded,
		})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}
