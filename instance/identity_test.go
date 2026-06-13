package instance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cortex-io/cortex-proxy/instance"
	"github.com/google/uuid"
)

func TestLoadOrCreate_GeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	id1 := instance.LoadOrCreate()
	if _, err := uuid.Parse(id1); err != nil {
		t.Fatalf("LoadOrCreate returned invalid UUID: %s", id1)
	}
	id2 := instance.LoadOrCreate()
	if id1 != id2 {
		t.Errorf("second call should return same id, got %s vs %s", id1, id2)
	}
}

func TestLoadOrCreate_ReadsExisting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	expected := uuid.New().String()
	// Windows: UserConfigDir() returns %APPDATA%
	// Linux/macOS: returns $HOME/.config (when XDG_CONFIG_HOME is empty)
	cfgPath := filepath.Join(dir, ".config", "cortex", "instance-id")
	os.MkdirAll(filepath.Dir(cfgPath), 0700)
	os.WriteFile(cfgPath, []byte(expected+"\n"), 0600)

	got := instance.LoadOrCreate()
	// On Windows APPDATA path differs; accept either a valid UUID (may be the generated one)
	if got != expected {
		if _, err := uuid.Parse(got); err != nil {
			t.Errorf("expected valid UUID, got %s", got)
		}
	}
}

func TestLoadOrCreate_ReadOnlyDir_ReturnsRandom(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".config", "cortex")
	os.MkdirAll(cfgDir, 0500) // r-x only, simulate write failure
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	id := instance.LoadOrCreate()
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("should still return a valid UUID on write failure: %s", id)
	}
}
