package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// keepDays is the number of days to keep log files.
const keepDays = 7

type state struct {
	mu   sync.Mutex
	file *os.File
}

// Init opens a daily-rotated log file under logDir and tees all standard-library
// log output to both the file and stderr. The file is named
// cortex-proxy-YYYY-MM-DD.log; a new file is opened at each local midnight.
// Log files older than 7 days are deleted at startup and after each rotation.
//
// If logDir cannot be created or the initial log file cannot be opened, Init
// returns an error and leaves the global log output unchanged.
//
// The returned cleanup func must be called on shutdown — it stops the rotation
// goroutine and closes the current log file.
func Init(logDir string) (func(), error) {
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return func() {}, fmt.Errorf("create log dir %s: %w", logDir, err)
	}
	cleanOldLogs(logDir)

	f, err := openDay(logDir, time.Now())
	if err != nil {
		return func() {}, fmt.Errorf("open log file: %w", err)
	}

	st := &state{file: f}
	log.SetOutput(io.MultiWriter(os.Stderr, f))

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-time.After(time.Until(nextMidnight())):
				st.rotate(logDir)
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
		<-done
		st.mu.Lock()
		defer st.mu.Unlock()
		log.SetOutput(os.Stderr)
		st.file.Close()
	}, nil
}

func (st *state) rotate(logDir string) {
	newF, err := openDay(logDir, time.Now())
	if err != nil {
		log.Printf("[WARN]  logger: rotate failed: %v", err)
		return
	}
	st.mu.Lock()
	old := st.file
	st.file = newF
	log.SetOutput(io.MultiWriter(os.Stderr, newF))
	st.mu.Unlock()
	old.Close()
	cleanOldLogs(logDir)
}

func openDay(logDir string, t time.Time) (*os.File, error) {
	name := "cortex-proxy-" + t.Format("2006-01-02") + ".log"
	return os.OpenFile(filepath.Join(logDir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
}

// cleanOldLogs deletes log files named cortex-proxy-YYYY-MM-DD.log that are
// older than keepDays days. Files from exactly keepDays ago are kept.
func cleanOldLogs(logDir string) {
	now := time.Now()
	// Truncate to start-of-day so the file dated exactly keepDays ago is kept.
	cutoff := time.Date(now.Year(), now.Month(), now.Day()-keepDays, 0, 0, 0, 0, now.Location())
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}
	const prefix = "cortex-proxy-"
	const suffix = ".log"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		dateStr := name[len(prefix) : len(name)-len(suffix)]
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil || !t.Before(cutoff) {
			continue
		}
		os.Remove(filepath.Join(logDir, name))
	}
}

func nextMidnight() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
}
