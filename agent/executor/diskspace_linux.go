//go:build linux

package executor

import (
	"fmt"
	"syscall"
)

func checkDiskSpace(path string, fileSize int64, threshold float64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return fmt.Errorf("statfs: %w", err)
	}
	freeBytes := int64(stat.Bavail) * int64(stat.Bsize) //nolint:gosec // overflow unlikely for filesystem sizes
	maxSize := int64(float64(freeBytes) * threshold)
	if fileSize > maxSize {
		return fmt.Errorf("insufficient_disk_space: file %d bytes exceeds threshold (%.0f%% of %d free)",
			fileSize, threshold*100, freeBytes)
	}
	return nil
}
