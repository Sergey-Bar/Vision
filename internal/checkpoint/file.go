package checkpoint

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileSync writes a file and flushes it to stable storage.
//
// The fsync is not decoration. A seal that is acknowledged but still sitting in
// the page cache when the host loses power is a seal that does not exist, and
// the whole point of this sink is to survive exactly that.
func writeFileSync(path string, body []byte, perm os.FileMode, doSync bool) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp seal: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("chmod temp seal: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		return fmt.Errorf("write temp seal: %w", err)
	}
	if doSync {
		if err := tmp.Sync(); err != nil {
			return fmt.Errorf("sync temp seal: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp seal: %w", err)
	}
	// Rename is atomic on every filesystem this product targets, so a reader
	// never observes a half-written seal under the real name.
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publish seal: %w", err)
	}
	if doSync {
		if d, err := os.Open(dir); err == nil {
			defer d.Close()
			_ = d.Sync()
		}
	}
	return nil
}
