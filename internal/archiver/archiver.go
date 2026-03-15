package archiver

import (
	"fmt"
	"strings"
	"time"

	"imap-archiver/internal/config"
	imapwrap "imap-archiver/internal/imapclient"
	"imap-archiver/internal/logger"

	imap "github.com/emersion/go-imap/v2"
)

// Stats tracks archive operation results.
type Stats struct {
	Folder   string
	Examined int
	Archived int
	Skipped  int
	Errors   int
}

// Archiver orchestrates the archive operation.
type Archiver struct {
	cfg    *config.Config
	client *imapwrap.Client
	log    *logger.Logger
}

// New creates a connected Archiver.
func New(cfg *config.Config) (*Archiver, error) {
	log := logger.New(cfg.Verbose)

	if cfg.DryRun {
		log.Info("DRY RUN mode — no messages will be moved")
	}

	client, err := imapwrap.Connect(cfg, log)
	if err != nil {
		return nil, err
	}

	log.Debug("Server capabilities: %s", strings.Join(client.Capabilities(), " "))

	return &Archiver{
		cfg:    cfg,
		client: client,
		log:    log,
	}, nil
}

// Close disconnects the IMAP session.
func (a *Archiver) Close() {
	a.client.Close()
}

// Run archives all configured folders.
func (a *Archiver) Run() error {
	cutoff := time.Now().Add(-a.cfg.MaxAge)
	a.log.Info("Archiving messages older than %s (cutoff: %s)",
		formatDuration(a.cfg.MaxAge), cutoff.Format("2006-01-02"))
	a.log.Info("Archive root: %s", a.cfg.ArchiveRoot)
	a.log.Info("Batch size: %d", a.cfg.BatchSize)

	var totalStats []Stats

	for _, folder := range a.cfg.Folders {
		a.log.Section(fmt.Sprintf("Folder: %s", folder))
		stats, err := a.archiveFolder(folder, cutoff)
		if err != nil {
			a.log.Error("Failed to archive folder %q: %v", folder, err)
			stats.Errors++
		}
		totalStats = append(totalStats, stats)
	}

	a.printSummary(totalStats)
	return nil
}

// archiveFolder archives a single source folder.
func (a *Archiver) archiveFolder(folder string, cutoff time.Time) (Stats, error) {
	stats := Stats{Folder: folder}

	// Search for messages before cutoff.
	uids, err := a.client.SearchOlderThan(folder, cutoff)
	if err != nil {
		return stats, fmt.Errorf("search: %w", err)
	}

	a.log.Info("Found %d message(s) older than cutoff in %q", len(uids), folder)
	stats.Examined = len(uids)

	if len(uids) == 0 {
		return stats, nil
	}

	// Fetch INTERNALDATE for all candidates so we can bucket by year.
	a.log.Info("Fetching dates for %d message(s)…", len(uids))
	dates, err := a.client.FetchInternalDate(uids)
	if err != nil {
		return stats, fmt.Errorf("fetch dates: %w", err)
	}

	// Group UIDs by target archive folder (Archives/YYYY/folder-path).
	byDest := make(map[string][]imap.UID)
	for _, uid := range uids {
		date, ok := dates[uid]
		if !ok {
			a.log.Warn("No date for UID %d — skipping", uid)
			stats.Skipped++
			continue
		}
		dest := a.destFolder(folder, date.Year())
		byDest[dest] = append(byDest[dest], uid)
	}

	// Move in batches, grouped by destination year-folder.
	for dest, destUIDs := range byDest {
		a.log.Info("→ Destination: %s (%d messages)", dest, len(destUIDs))

		if !a.cfg.DryRun {
			if err := a.client.EnsureFolder(dest); err != nil {
				a.log.Error("Cannot create folder %q: %v", dest, err)
				stats.Errors += len(destUIDs)
				continue
			}
		}

		batches := splitBatches(destUIDs, a.cfg.BatchSize)
		for i, batch := range batches {
			a.log.Info("  Batch %d/%d: moving %d message(s)…",
				i+1, len(batches), len(batch))

			if a.cfg.DryRun {
				a.log.Info("  [DRY RUN] Would move UIDs: %s", formatUIDs(batch, 10))
				stats.Archived += len(batch)
				continue
			}

			if err := a.client.MoveUIDs(batch, dest); err != nil {
				a.log.Error("  Move failed: %v", err)
				stats.Errors += len(batch)
				continue
			}
			stats.Archived += len(batch)
			a.log.Info("  ✓ Moved %d message(s)", len(batch))
		}
	}

	return stats, nil
}

// destFolder builds the archive destination path.
// e.g. folder="Work/Projects", year=2022 → "Archives/2022/Work/Projects"
func (a *Archiver) destFolder(sourceFolder string, year int) string {
	return fmt.Sprintf("%s/%d/%s", a.cfg.ArchiveRoot, year, sourceFolder)
}

// printSummary prints a table of results.
func (a *Archiver) printSummary(allStats []Stats) {
	a.log.Section("Summary")
	fmt.Printf("%-40s  %8s  %8s  %8s  %8s\n",
		"Folder", "Examined", "Archived", "Skipped", "Errors")
	fmt.Println(strings.Repeat("─", 80))

	totalExamined, totalArchived, totalSkipped, totalErrors := 0, 0, 0, 0
	for _, s := range allStats {
		fmt.Printf("%-40s  %8d  %8d  %8d  %8d\n",
			truncate(s.Folder, 40), s.Examined, s.Archived, s.Skipped, s.Errors)
		totalExamined += s.Examined
		totalArchived += s.Archived
		totalSkipped += s.Skipped
		totalErrors += s.Errors
	}

	fmt.Println(strings.Repeat("─", 80))
	fmt.Printf("%-40s  %8d  %8d  %8d  %8d\n",
		"TOTAL", totalExamined, totalArchived, totalSkipped, totalErrors)
}

// splitBatches splits a UID slice into chunks of at most size n.
func splitBatches(uids []imap.UID, n int) [][]imap.UID {
	var batches [][]imap.UID
	for len(uids) > 0 {
		size := n
		if size > len(uids) {
			size = len(uids)
		}
		batches = append(batches, uids[:size])
		uids = uids[size:]
	}
	return batches
}

// formatDuration renders a duration as a human string.
func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days >= 365 && days%365 == 0:
		return fmt.Sprintf("%d year(s)", days/365)
	case days >= 30:
		return fmt.Sprintf("%d day(s) (~%.0f month(s))", days, float64(days)/30.44)
	default:
		return fmt.Sprintf("%d day(s)", days)
	}
}

// formatUIDs renders up to max UIDs as a compact string.
func formatUIDs(uids []imap.UID, max int) string {
	var sb strings.Builder
	for i, uid := range uids {
		if i >= max {
			fmt.Fprintf(&sb, "… +%d more", len(uids)-max)
			break
		}
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%d", uid)
	}
	return sb.String()
}

// truncate shortens a string to n runes.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
