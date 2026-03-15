package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds all runtime configuration.
type Config struct {
	Host        string
	Port        int
	Username    string
	Password    string
	TLS         bool
	StartTLS    bool
	Folders     []string
	ArchiveRoot string
	MaxAge      time.Duration
	BatchSize   int
	DryRun      bool
	Verbose     bool
	Help        bool
}

// Parse parses command-line arguments and returns a Config.
func Parse(args []string) (*Config, error) {
	fs := flag.NewFlagSet("imap-archiver", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	host := fs.String("host", "", "IMAP server hostname (required)")
	port := fs.Int("port", 0, "IMAP server port (default: 993 with TLS, 143 without)")
	username := fs.String("user", "", "IMAP username / email address (required)")
	password := fs.String("pass", "", "IMAP password (or set IMAP_PASSWORD env var)")
	tls := fs.Bool("tls", true, "Use implicit TLS (IMAPS)")
	startTLS := fs.Bool("starttls", false, "Use STARTTLS upgrade (overrides -tls)")
	folders := fs.String("folders", "INBOX", "Comma-separated list of folders to archive")
	archiveRoot := fs.String("archive-root", "Archives", "Root folder for archived mail (e.g. Archives)")
	ageDays := fs.Int("age", 365, "Archive messages older than this many days")
	batchSize := fs.Int("batch", 1000, "Maximum messages to move per batch")
	dryRun := fs.Bool("dry-run", false, "Show what would be archived without moving anything")
	verbose := fs.Bool("v", false, "Verbose output")
	help := fs.Bool("h", false, "Show help")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return &Config{Help: true}, nil
		}
		return nil, err
	}

	cfg := &Config{
		Host:        *host,
		Port:        *port,
		Username:    *username,
		Password:    *password,
		TLS:         *tls,
		StartTLS:    *startTLS,
		ArchiveRoot: strings.TrimRight(*archiveRoot, "/"),
		MaxAge:      time.Duration(*ageDays) * 24 * time.Hour,
		BatchSize:   *batchSize,
		DryRun:      *dryRun,
		Verbose:     *verbose,
		Help:        *help,
	}

	if cfg.Help {
		return cfg, nil
	}

	// Password from env if not provided via flag.
	if cfg.Password == "" {
		cfg.Password = os.Getenv("IMAP_PASSWORD")
	}

	// Validate required fields.
	if cfg.Host == "" {
		return nil, fmt.Errorf("--host is required")
	}
	if cfg.Username == "" {
		return nil, fmt.Errorf("--user is required")
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("--pass is required (or set IMAP_PASSWORD env var)")
	}
	if cfg.BatchSize < 1 {
		return nil, fmt.Errorf("--batch must be >= 1")
	}

	// Default port based on TLS mode.
	if cfg.Port == 0 {
		if cfg.StartTLS {
			cfg.Port = 143
		} else if cfg.TLS {
			cfg.Port = 993
		} else {
			cfg.Port = 143
		}
	}

	// Parse folder list.
	for _, f := range strings.Split(*folders, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			cfg.Folders = append(cfg.Folders, f)
		}
	}
	if len(cfg.Folders) == 0 {
		cfg.Folders = []string{"INBOX"}
	}

	return cfg, nil
}

// PrintUsage prints help text.
func PrintUsage() {
	fmt.Print(`imap-archiver — Move old IMAP messages into yearly archive folders.

USAGE
  imap-archiver [OPTIONS]

REQUIRED FLAGS
  --host <host>       IMAP server hostname
  --user <user>       IMAP username / email address
  --pass <pass>       IMAP password  (or set env var IMAP_PASSWORD)

OPTIONS
  --port <port>       IMAP port (default 993 with TLS, 143 without)
  --tls               Use implicit TLS / IMAPS (default true)
  --starttls          Use STARTTLS instead of implicit TLS
  --folders <list>    Comma-separated source folders (default: INBOX)
  --archive-root <f>  Root archive folder name (default: Archives)
  --age <days>        Archive messages older than N days (default: 365)
  --batch <n>         Max messages per batch move (default: 1000)
  --dry-run           Preview only — do not move any messages
  -v                  Verbose output
  -h                  Show this help

ARCHIVE LAYOUT
  Messages are moved to:
    <archive-root>/<YYYY>/<source-folder>

  Example — a 2022 message from "Work/Projects" moves to:
    Archives/2022/Work/Projects

EXAMPLES
  # Archive INBOX messages older than 1 year (default)
  imap-archiver --host mail.example.com --user me@example.com --pass secret

  # Archive several folders, older than 6 months, dry-run first
  imap-archiver --host imap.gmail.com --user me@gmail.com \
      --pass secret --folders "INBOX,Sent,Work" --age 180 --dry-run

  # Use environment variable for password
  export IMAP_PASSWORD=secret
  imap-archiver --host imap.fastmail.com --user me@fastmail.com \
      --folders "INBOX,Archives/Inbox" --archive-root OldMail
`)
}
