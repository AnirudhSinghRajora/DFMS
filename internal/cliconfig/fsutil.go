package cliconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path atomically: it writes to a temporary file
// in the same directory (so the rename is atomic) with the given permissions,
// then renames it over path. A crash mid-write therefore cannot leave a
// truncated file in place of a previously valid one. The parent directory is
// created with dirPerm if needed.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup; a no-op once the rename below succeeds.
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing data: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
