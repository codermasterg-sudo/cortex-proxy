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
	t.Setenv("USERPROFILE", dir) // Windows fallback

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
	t.Setenv("USERPROFILE", dir) // Windows fallback

	expected := uuid.New().String()
	cfgPath := filepath.Join(dir, ".cortex-proxy", "instance-id")
	os.MkdirAll(filepath.Dir(cfgPath), 0700)
	os.WriteFile(cfgPath, []byte(expected+"\n"), 0600)

	got := instance.LoadOrCreate()
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestLoadOrCreate_ReadOnlyDir_ReturnsRandom(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".cortex-proxy")
	os.MkdirAll(cfgDir, 0500) // r-x only, simulate write failure
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // Windows fallback

	id := instance.LoadOrCreate()
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("should still return a valid UUID on write failure: %s", id)
	}
}
