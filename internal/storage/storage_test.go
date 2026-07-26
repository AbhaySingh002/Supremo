package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicPreservesPermissionsAndFailedTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("old"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("permissions changed: info=%v err=%v", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("unexpected content: %q, %v", data, err)
	}

	target := filepath.Join(dir, "existing-directory")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(target, []byte("replacement"), 0600); err == nil {
		t.Fatal("expected directory replacement to fail")
	}
	data, err = os.ReadFile(marker)
	if err != nil || string(data) != "keep" {
		t.Fatalf("failed write altered existing target: %q, %v", data, err)
	}
}
