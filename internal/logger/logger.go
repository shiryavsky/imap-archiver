package logger

import (
	"fmt"
	"os"
	"time"
)

// Logger is a simple leveled logger.
type Logger struct {
	verbose bool
}

// New creates a Logger.
func New(verbose bool) *Logger {
	return &Logger{verbose: verbose}
}

func ts() string {
	return time.Now().Format("15:04:05")
}

// Info logs an informational message.
func (l *Logger) Info(format string, args ...any) {
	fmt.Fprintf(os.Stdout, "[%s] INFO  %s\n", ts(), fmt.Sprintf(format, args...))
}

// Debug logs only when verbose is enabled.
func (l *Logger) Debug(format string, args ...any) {
	if l.verbose {
		fmt.Fprintf(os.Stdout, "[%s] DEBUG %s\n", ts(), fmt.Sprintf(format, args...))
	}
}

// Warn logs a warning.
func (l *Logger) Warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[%s] WARN  %s\n", ts(), fmt.Sprintf(format, args...))
}

// Error logs an error.
func (l *Logger) Error(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[%s] ERROR %s\n", ts(), fmt.Sprintf(format, args...))
}

// Section prints a section divider.
func (l *Logger) Section(title string) {
	fmt.Fprintf(os.Stdout, "\n[%s] ─── %s ───\n", ts(), title)
}
