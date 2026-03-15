package main

import (
	"fmt"
	"os"

	"imap-archiver/internal/archiver"
	"imap-archiver/internal/config"
)

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		config.PrintUsage()
		os.Exit(1)
	}

	if cfg.Help {
		config.PrintUsage()
		os.Exit(0)
	}

	a, err := archiver.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize archiver: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	if err := a.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Archive failed: %v\n", err)
		os.Exit(1)
	}
}
