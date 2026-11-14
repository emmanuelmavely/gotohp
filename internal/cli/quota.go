package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"app/core"

	"github.com/spf13/cobra"
)

// newQuotaCommand builds the `quota` command group for inspecting the remote
// storage that counts against the account's Google Photos quota.
func newQuotaCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quota",
		Short: "Inspect remote media that counts against the storage quota",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			configPath, _ := cmd.Flags().GetString("config")
			if err := core.LoadConfig(configPath); err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			return nil
		},
	}
	f := cmd.PersistentFlags()
	f.String("account", "", "account email to query (default: the selected account)")
	f.String("proxy", "", "proxy URL for all requests")

	cmd.AddCommand(newQuotaListCommand())
	return cmd
}

func newQuotaListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List remote media that consumes storage quota",
		Long: `List remote media that consumes storage quota.

Syncs the whole remote library and prints every file that charges the
account's storage quota, including files sitting in the trash (those still
count against the quota until they are deleted permanently).

A file that appears in the library more than once is listed and counted
once; the COPIES column shows how many library entries point at it. Sizes
use binary units (1 GB = 1024 MB), like Google's storage page.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			account, _ := cmd.PersistentFlags().GetString("account")
			proxy, _ := cmd.PersistentFlags().GetString("proxy")
			asJSON, _ := f.GetBool("json")

			api, err := core.NewApi(core.ApiOptions{Account: account, Proxy: proxy})
			if err != nil {
				return fmt.Errorf("failed to create api client: %w", err)
			}
			// Progress goes to stderr so --json output on stdout stays clean.
			// The line is redrawn in place on a terminal and skipped when
			// stderr is redirected.
			live := isTerminal(os.Stderr)
			started := time.Now()
			// The library index cache turns a repeat run into a delta sync:
			// only the changes since the last sync are fetched.
			cached, _ := core.LoadLibraryIndex()
			verb := "Syncing"
			if cached != nil && cached.SyncToken != "" {
				verb = "Updating"
			}
			index, err := api.SyncLibraryIncremental(cached, func(s core.LibrarySyncStats) {
				if live {
					fmt.Fprintf(os.Stderr, "\r%-100s", syncProgressLine(verb, s, time.Since(started)))
				}
			})
			if live {
				fmt.Fprintf(os.Stderr, "\r%-100s\r", "")
			}
			if err != nil {
				return fmt.Errorf("failed to list quota-charged media: %w", err)
			}
			if err := index.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "note: could not save the library index: %v\n", err)
			}
			fmt.Fprintln(os.Stderr, syncProgressLine("Synced", core.LibrarySyncStats{
				Items:        len(index.Items),
				ChargedItems: len(core.QuotaChargedMedia(index.Items)),
			}, time.Since(started)))
			library := index.Items
			entries := core.QuotaChargedMedia(library)
			if breakdown, _ := f.GetBool("breakdown"); breakdown {
				printQuotaBreakdown(entries, core.SummarizeQuota(entries))
				return nil
			}

			// The library repeats a file once per place it appears; the
			// account is charged for it once, so list and total unique files.
			items, copies := core.UniqueQuotaChargedMedia(entries)
			core.SortRemoteMediaItems(items)
			summary := core.SummarizeQuota(items)

			if asJSON {
				printQuotaJSON(items, copies, len(entries), summary)
				return nil
			}
			printQuotaTable(items, copies, len(entries), summary)
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "print machine-readable JSON")
	cmd.Flags().Bool("breakdown", false, "print totals of all library entries grouped by origin, duplicate, trash and quality")
	return cmd
}

// syncProgressLine describes how far the library walk has got.
func syncProgressLine(verb string, s core.LibrarySyncStats, elapsed time.Duration) string {
	return fmt.Sprintf("%s: page %d, %d items (%s), %d count against quota (%s), %s",
		verb, s.Pages, s.Items, humanBytes(s.TotalBytes),
		s.ChargedItems, humanBytes(s.ChargedBytes), elapsed.Round(time.Second))
}

// isTerminal reports whether f is an interactive terminal.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// printQuotaTable renders the quota-charged files as a plain text table,
// largest quota charge first.
func printQuotaTable(items []*core.RemoteMediaItem, copies map[string]int, entryCount int, summary core.QuotaSummary) {
	if summary.ItemCount == 0 {
		fmt.Println("? No media in the library consumes storage quota")
		return
	}
	fmt.Printf("? %d files consume %s of storage quota\n", summary.ItemCount, humanBytes(summary.TotalBytes))
	if entryCount > summary.ItemCount {
		fmt.Printf("  (%d library entries; copies of the same file are counted once)\n", entryCount)
	}
	fmt.Println()

	sizeWidth := len("SIZE")
	nameWidth := len("FILE")
	for _, item := range items {
		if w := len(humanBytes(item.QuotaChargedBytes)); w > sizeWidth {
			sizeWidth = w
		}
		if w := len(item.FileName); w > nameWidth && w <= 60 {
			nameWidth = w
		}
	}
	fmt.Printf("  %*s  %-6s  %-7s  %-*s  %s\n", sizeWidth, "SIZE", "COPIES", "TRASHED", nameWidth, "FILE", "MEDIA KEY")
	for _, item := range items {
		name := item.FileName
		if len(name) > 60 {
			name = name[:57] + "..."
		}
		trashed := ""
		if item.Trashed {
			trashed = "yes"
		}
		copyCount := ""
		if n := copies[item.DedupKey]; n > 1 {
			copyCount = fmt.Sprintf("%d", n)
		}
		fmt.Printf("  %*s  %-6s  %-7s  %-*s  %s\n",
			sizeWidth, humanBytes(item.QuotaChargedBytes), copyCount, trashed, nameWidth, name, item.MediaKey)
	}

	if summary.TrashedCount > 0 {
		fmt.Printf("\n  %d of these files are in the trash and still consume %s.\n"+
			"  They stop counting against the quota only after being deleted permanently.\n",
			summary.TrashedCount, humanBytes(summary.TrashedBytes))
	}
}

// originName names the origin codes reported by the library sync.
func originName(origin int64) string {
	switch origin {
	case 0, core.OriginSelf: // the server omits the field for own items
		return "self"
	case core.OriginPartner:
		return "partner"
	case core.OriginShared:
		return "shared"
	default:
		return fmt.Sprintf("other(%d)", origin)
	}
}

// printQuotaBreakdown groups the quota-charged items by the properties that
// could explain a difference from the usage Google reports: whose item it
// is, whether it is a duplicate, whether it is trashed, and its quality.
func printQuotaBreakdown(items []*core.RemoteMediaItem, summary core.QuotaSummary) {
	type group struct {
		origin, canonical, trashed, quality string
		items                               int
		bytes                               int64
	}
	yesNo := func(v bool) string {
		if v {
			return "yes"
		}
		return "no"
	}

	groups := map[string]*group{}
	seenDedup := map[string]bool{}
	var uniqueItems int
	var uniqueBytes int64
	for _, item := range items {
		quality := "saver"
		if item.OriginalQuality {
			quality = "original"
		}
		g := group{origin: originName(item.Origin), canonical: yesNo(item.IsCanonical), trashed: yesNo(item.Trashed), quality: quality}
		key := g.origin + "|" + g.canonical + "|" + g.trashed + "|" + g.quality
		if groups[key] == nil {
			groups[key] = &g
		}
		groups[key].items++
		groups[key].bytes += item.QuotaChargedBytes

		if !seenDedup[item.DedupKey] {
			seenDedup[item.DedupKey] = true
			uniqueItems++
			uniqueBytes += item.QuotaChargedBytes
		}
	}

	rows := make([]*group, 0, len(groups))
	for _, g := range groups {
		rows = append(rows, g)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].bytes != rows[j].bytes {
			return rows[i].bytes > rows[j].bytes
		}
		return rows[i].origin+rows[i].canonical+rows[i].trashed+rows[i].quality <
			rows[j].origin+rows[j].canonical+rows[j].trashed+rows[j].quality
	})

	fmt.Printf("%d items report %s of quota-charged bytes\n\n", summary.ItemCount, humanBytes(summary.TotalBytes))
	fmt.Printf("  %-10s  %-9s  %-7s  %-8s  %7s  %8s\n", "ORIGIN", "CANONICAL", "TRASHED", "QUALITY", "ITEMS", "SIZE")
	for _, g := range rows {
		fmt.Printf("  %-10s  %-9s  %-7s  %-8s  %7d  %8s\n", g.origin, g.canonical, g.trashed, g.quality, g.items, humanBytes(g.bytes))
	}
	fmt.Printf("\n  Counting each file once (by dedup key): %d items, %s\n", uniqueItems, humanBytes(uniqueBytes))
}

// humanBytes formats a byte count in binary units (1 GB = 1024 MB) under the
// usual KB/MB/GB labels, which is how Google's storage page counts, so the
// totals can be compared with it directly.
func humanBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			switch {
			case value < 10 && (suffix == "GB" || suffix == "TB"):
				return fmt.Sprintf("%.2f %s", value, suffix)
			case value < 10:
				return fmt.Sprintf("%.1f %s", value, suffix)
			default:
				return fmt.Sprintf("%.0f %s", value, suffix)
			}
		}
	}
	return fmt.Sprintf("%.0f PB", value/unit)
}

func printQuotaJSON(items []*core.RemoteMediaItem, copies map[string]int, entryCount int, summary core.QuotaSummary) {
	type quotaItem struct {
		MediaKey          string `json:"mediaKey"`
		FileName          string `json:"fileName"`
		DedupKey          string `json:"dedupKey"`
		QuotaChargedBytes int64  `json:"quotaChargedBytes"`
		Trashed           bool   `json:"trashed"`
		TrashTimestamp    int64  `json:"trashTimestamp,omitempty"`
		OriginalQuality   bool   `json:"originalQuality"`
		Type              int64  `json:"type"`
		Origin            string `json:"origin"`
		IsCanonical       bool   `json:"isCanonical"`
		// Copies is the number of library entries pointing at this file.
		Copies int `json:"copies"`
	}
	out := struct {
		Items   []quotaItem       `json:"items"`
		Summary core.QuotaSummary `json:"summary"`
		// LibraryEntries counts the quota-charged entries before copies of
		// the same file were collapsed.
		LibraryEntries int `json:"libraryEntries"`
	}{Items: make([]quotaItem, 0, len(items)), Summary: summary, LibraryEntries: entryCount}
	for _, item := range items {
		out.Items = append(out.Items, quotaItem{
			MediaKey:          item.MediaKey,
			FileName:          item.FileName,
			DedupKey:          item.DedupKey,
			QuotaChargedBytes: item.QuotaChargedBytes,
			Trashed:           item.Trashed,
			TrashTimestamp:    item.TrashTimestamp,
			OriginalQuality:   item.OriginalQuality,
			Type:              item.Type,
			Origin:            originName(item.Origin),
			IsCanonical:       item.IsCanonical,
			Copies:            copies[item.DedupKey],
		})
	}
	jsonOutput, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		jsonOutput = []byte(fmt.Sprintf("error generating JSON: %v", err))
	}
	fmt.Println(string(jsonOutput))
}
