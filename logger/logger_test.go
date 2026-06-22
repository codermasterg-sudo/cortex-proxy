package logger

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInit_CreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	cleanup, err := Init(logDir)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer cleanup()

	expected := "cortex-proxy-" + time.Now().Format("2006-01-02") + ".log"
	entries, _ := os.ReadDir(logDir)
	found := false
	for _, e := range entries {
		if e.Name() == expected {
			found = true
		}
	}
	if !found {
		t.Errorf("expected log file %s not found in %s", expected, logDir)
	}
}

func TestInit_WritesToFile(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	cleanup, err := Init(logDir)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	const msg = "test-log-entry-xyz"
	log.Print(msg)
	cleanup()

	// Restore stderr output for subsequent tests.
	log.SetOutput(os.Stderr)

	name := "cortex-proxy-" + time.Now().Format("2006-01-02") + ".log"
	data, err := os.ReadFile(filepath.Join(logDir, name))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), msg) {
		t.Errorf("log file does not contain %q; got: %s", msg, string(data))
	}
}

func TestCleanOldLogs_RemovesOldFiles(t *testing.T) {
	dir := t.TempDir()

	old := time.Now().AddDate(0, 0, -(keepDays + 1))
	recent := time.Now().AddDate(0, 0, -1)

	oldName := "cortex-proxy-" + old.Format("2006-01-02") + ".log"
	recentName := "cortex-proxy-" + recent.Format("2006-01-02") + ".log"
	unrelatedName := "other.log"

	for _, name := range []string{oldName, recentName, unrelatedName} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0640)
	}

	cleanOldLogs(dir)

	if _, err := os.Stat(filepath.Join(dir, oldName)); !os.IsNotExist(err) {
		t.Errorf("expected old file %s to be deleted", oldName)
	}
	if _, err := os.Stat(filepath.Join(dir, recentName)); err != nil {
		t.Errorf("expected recent file %s to be kept: %v", recentName, err)
	}
	if _, err := os.Stat(filepath.Join(dir, unrelatedName)); err != nil {
		t.Errorf("expected unrelated file %s to be kept: %v", unrelatedName, err)
	}
}

func TestCleanOldLogs_KeepsExactBoundary(t *testing.T) {
	dir := t.TempDir()

	// File exactly at the boundary (keepDays ago today) should be kept.
	boundary := time.Now().AddDate(0, 0, -keepDays)
	name := "cortex-proxy-" + boundary.Format("2006-01-02") + ".log"
	os.WriteFile(filepath.Join(dir, name), []byte("x"), 0640)

	cleanOldLogs(dir)

	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Errorf("boundary file %s should be kept: %v", name, err)
	}
}

func TestNextMidnight(t *testing.T) {
	m := nextMidnight()
	now := time.Now()
	if !m.After(now) {
		t.Errorf("nextMidnight %v is not after now %v", m, now)
	}
	if m.Hour() != 0 || m.Minute() != 0 || m.Second() != 0 {
		t.Errorf("nextMidnight should be at 00:00:00, got %v", m)
	}
	if m.Sub(now) > 24*time.Hour {
		t.Errorf("nextMidnight is more than 24h away: %v", m.Sub(now))
	}
}
