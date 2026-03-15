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

// Ensure imap import is used (for splitBatches / formatUIDs which take []imap.UID).
var _ = imap.UID(0)

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
	a.log.Info("Batch size:   %d", a.cfg.BatchSize)

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

// archiveFolder archives one source folder.
//
// Strategy: instead of a single SEARCH BEFORE + mass FETCH INTERNALDATE,
// we issue one lightweight SEARCH per calendar year (SINCE Jan1 BEFORE Jan1+1).
// The server does the date math; we receive UIDs pre-bucketed by year with zero
// FETCH traffic. For 20k messages across 5 years this means 5 tiny SEARCH
// round-trips instead of one giant FETCH response.
func (a *Archiver) archiveFolder(folder string, cutoff time.Time) (Stats, error) {
	stats := Stats{Folder: folder}

	// Select the folder once; all subsequent searches reuse this selection.
	if err := a.client.SelectFolder(folder); err != nil {
		return stats, err
	}

	// Sweep calendar years using server-side SEARCH SINCE/BEFORE.
	// minYear goes back far enough to catch all realistic mail (30 years).
	minYear := time.Now().Year() - 30
	buckets, err := a.client.SearchByYearRange(cutoff, minYear)
	if err != nil {
		return stats, fmt.Errorf("year-range search: %w", err)
	}

	if len(buckets) == 0 {
		a.log.Info("No messages older than cutoff found in %q", folder)
		return stats, nil
	}

	// Log a compact summary before we start moving.
	total := 0
	for _, b := range buckets {
		total += len(b.UIDs)
		a.log.Info("  Year %d: %d message(s)", b.Year, len(b.UIDs))
	}
	a.log.Info("Total to archive: %d message(s) across %d year(s)", total, len(buckets))
	stats.Examined = total

	// Process each year-bucket independently.
	for _, bucket := range buckets {
		dest := a.destFolder(folder, bucket.Year)
		s, err := a.moveBucket(bucket, dest, folder)
		stats.Archived += s.Archived
		stats.Skipped += s.Skipped
		stats.Errors += s.Errors
		if err != nil {
			a.log.Error("Year %d: %v", bucket.Year, err)
		}
	}

	return stats, nil
}

// moveBucket moves all UIDs in a YearBucket to dest, in batches.
func (a *Archiver) moveBucket(bucket imapwrap.YearBucket, dest string, sourceFolder string) (Stats, error) {
	var stats Stats
	a.log.Info("→ %s (%d messages)", dest, len(bucket.UIDs))

	if !a.cfg.DryRun {
		if err := a.client.EnsureFolder(dest); err != nil {
			stats.Errors += len(bucket.UIDs)
			return stats, fmt.Errorf("create folder %q: %w", dest, err)
		}
		// Re-select the source folder after EnsureFolder may have changed it.
		// EnsureFolder ends by selecting INBOX, which breaks subsequent COPY/MOVE.
		if err := a.client.SelectFolder(sourceFolder); err != nil {
			stats.Errors += len(bucket.UIDs)
			return stats, fmt.Errorf("re-select folder %q: %w", sourceFolder, err)
		}
	}

	batches := splitBatches(bucket.UIDs, a.cfg.BatchSize)
	for i, batch := range batches {
		a.log.Info("  Batch %d/%d: %d message(s)…", i+1, len(batches), len(batch))

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
