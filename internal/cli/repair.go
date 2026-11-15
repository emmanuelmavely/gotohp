package cli

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"app/core"

	"github.com/spf13/cobra"
)

// newRepairCommand builds the `repair` command group for fixing remote
// library inconsistencies.
func newRepairCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Repair remote library inconsistencies",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			configPath, _ := cmd.Flags().GetString("config")
			if err := core.LoadConfig(configPath); err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			return nil
		},
	}
	f := cmd.PersistentFlags()
	f.String("account", "", "account email to repair (default: the selected account)")
	f.String("proxy", "", "proxy URL for all requests")

	cmd.AddCommand(newRepairLivePhotosCommand())
	return cmd
}

// newRepairLivePhotosCommand implements `repair live-photos`: orphan .mov
// files that sit in the library next to an unlinked still are downloaded,
// verified, re-uploaded, linked to the still, and trashed. The default run
// only reports what it found; --execute performs the repairs.
func newRepairLivePhotosCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "live-photos",
		Short: "Link orphan Live Photo videos to their photos",
		Long: `Link orphan Live Photo videos to their photos.

When Live Photos were uploaded unpaired, the library ends up with a still
and a separate .mov file that Google Photos never linked. This command
matches them by filename stem (IMG_0001.jpg + IMG_0001.mov), downloads the
stored video to verify its content, re-uploads it, links it to the still,
and moves the orphan library entry to the trash.

Without --execute the command only reports what it would repair.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			account, _ := cmd.PersistentFlags().GetString("account")
			proxy, _ := cmd.PersistentFlags().GetString("proxy")
			execute, _ := f.GetBool("execute")
			asJSON, _ := f.GetBool("json")

			api, err := core.NewApi(core.ApiOptions{Account: account, Proxy: proxy})
			if err != nil {
				return fmt.Errorf("failed to create api client: %w", err)
			}

			// The library index cache keeps repeat runs cheap.
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

			report := core.FindLivePhotoRepairReport(index.Items)
			candidates, ambiguous := report.Candidates, report.Ambiguous
			if asJSON {
				return printRepairJSON(candidates, ambiguous, report.Excluded, report.AlreadyRepaired)
			}
			for _, exclusion := range report.Excluded {
				fmt.Printf("? %s: skipped, %s\n", exclusion.Stem, exclusion.Reason)
			}
			for _, done := range report.AlreadyRepaired {
				fmt.Printf("? %s: already a Live Photo, %s\n", done.Stem, done.Reason)
			}

			for _, stem := range ambiguous {
				fmt.Printf("? %s: several photos or videos share this stem, skipped\n", stem)
			}
			if len(candidates) == 0 {
				fmt.Println("? No orphan Live Photo pairs found")
				return nil
			}
			fmt.Printf("? %d orphan Live Photo pair(s) found:\n", len(candidates))
			for _, candidate := range candidates {
				fmt.Printf("  %s: %s (%s) + %s (%s)\n",
					candidate.Stem,
					candidate.Photo.FileName, humanBytes(candidate.Photo.SizeBytes),
					candidate.Video.FileName, humanBytes(candidate.Video.SizeBytes))
			}
			if !execute {
				fmt.Println("\nThis was a report only. Run again with --execute to perform the repairs.")
				return nil
			}

			// One pair at a time; a live line on stderr follows each pair's
			// stages, and every result is printed as soon as it is known.
			printed := 0
			printOutcome := func(index, total int, fileName string, outcome core.LivePhotoRepairOutcome) {
				verb := outcome.Status
				if verb == "repaired" {
					verb = "done"
				}
				fmt.Printf("- [%d/%d] %s: %s: %s\n", index, total, fileName, verb, outcome.Message)
			}
			outcomes := api.RepairLivePhotoPairs(context.Background(), candidates, func(p core.LivePhotoRepairProgress) {
				if p.Stage != core.RepairStageDone {
					if live {
						fmt.Fprintf(os.Stderr, "\r%-100s", repairProgressLine(p))
					}
					return
				}
				if live {
					fmt.Fprintf(os.Stderr, "\r%-100s\r", "")
				}
				printOutcome(p.Index, p.Total, p.FileName, *p.Outcome)
				printed = p.Index
			})
			// Pairs the batch did not attempt (connection lost) have no event.
			for i := printed; i < len(outcomes); i++ {
				printOutcome(i+1, len(outcomes), candidates[i].Video.FileName, outcomes[i])
			}

			repaired, skipped, failed := 0, 0, 0
			for _, outcome := range outcomes {
				switch outcome.Status {
				case "repaired":
					repaired++
				case "skipped":
					skipped++
				default:
					failed++
				}
			}
			fmt.Printf("\n%d repaired, %d skipped, %d failed\n", repaired, skipped, failed)
			if failed > 0 {
				return exitError{code: 1}
			}
			return nil
		},
	}
	cmd.Flags().Bool("execute", false, "perform the repairs (default: report only)")
	cmd.Flags().Bool("json", false, "print machine-readable JSON (candidates only without --execute)")
	return cmd
}

type repairCandidateJSON struct {
	Stem        string `json:"stem"`
	PhotoKey    string `json:"photoKey"`
	PhotoName   string `json:"photoName"`
	PhotoSize   int64  `json:"photoSize"`
	VideoKey    string `json:"videoKey"`
	VideoName   string `json:"videoName"`
	VideoSize   int64  `json:"videoSize"`
	VideoDedup  string `json:"videoDedupKey"`
	PhotoDigest string `json:"photoSha1Hex"`
	VideoDigest string `json:"videoSha1Hex"`
}

func printRepairJSON(candidates []core.LivePhotoRepairCandidate, ambiguous []string, excluded []core.LivePhotoExclusion, alreadyRepaired []core.LivePhotoAlreadyRepaired) error {
	type exclusionJSON struct {
		Stem   string `json:"stem"`
		Reason string `json:"reason"`
	}
	out := struct {
		Candidates []repairCandidateJSON `json:"candidates"`
		Ambiguous  []string              `json:"ambiguous"`
		Excluded   []exclusionJSON       `json:"excluded"`
		Repaired   []exclusionJSON       `json:"alreadyRepaired"`
	}{Candidates: make([]repairCandidateJSON, 0, len(candidates)), Ambiguous: ambiguous, Excluded: make([]exclusionJSON, 0, len(excluded)), Repaired: make([]exclusionJSON, 0, len(alreadyRepaired))}
	for _, done := range alreadyRepaired {
		out.Repaired = append(out.Repaired, exclusionJSON{Stem: done.Stem, Reason: done.Reason})
	}
	for _, exclusion := range excluded {
		out.Excluded = append(out.Excluded, exclusionJSON{Stem: exclusion.Stem, Reason: exclusion.Reason})
	}
	for _, candidate := range candidates {
		out.Candidates = append(out.Candidates, repairCandidateJSON{
			Stem:        candidate.Stem,
			PhotoKey:    candidate.Photo.MediaKey,
			PhotoName:   candidate.Photo.FileName,
			PhotoSize:   candidate.Photo.SizeBytes,
			VideoKey:    candidate.Video.MediaKey,
			VideoName:   candidate.Video.FileName,
			VideoSize:   candidate.Video.SizeBytes,
			VideoDedup:  candidate.Video.DedupKey,
			PhotoDigest: hexDigest(candidate.Photo.Sha1),
			VideoDigest: hexDigest(candidate.Video.Sha1),
		})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

func hexDigest(digest []byte) string {
	return hex.EncodeToString(digest)
}

// repairProgressLine describes where the current repair stands.
func repairProgressLine(p core.LivePhotoRepairProgress) string {
	line := fmt.Sprintf("Repairing [%d/%d] %s: %s", p.Index, p.Total, p.FileName, p.Stage)
	if p.BytesTotal > 0 {
		line += fmt.Sprintf(" %s of %s", humanBytes(p.BytesDone), humanBytes(p.BytesTotal))
	} else if p.BytesDone > 0 {
		line += " " + humanBytes(p.BytesDone)
	}
	return line
}
