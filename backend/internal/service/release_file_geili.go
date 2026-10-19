package service

import (
	"os"
	"path/filepath"

	"github.com/Wei-Shaw/sub2api/internal/pkg/releaselifecycle"
)

// Pricing caches are shared by the two release processes. Readers must never
// observe an in-place truncate while a peer warms or refreshes its cache.
func writeReleaseFileGeili(path string, data []byte, mode os.FileMode) error {
	if !releaselifecycle.Default.Enabled() {
		return os.WriteFile(path, data, mode)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".geili-cache-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = file.Chmod(mode)
	if err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	return err
}
