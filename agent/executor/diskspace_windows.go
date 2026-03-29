//go:build windows

package executor

import "fmt"

func checkDiskSpace(_ string, _ int64, _ float64) error {
	// TODO: implement Windows free space check via GetDiskFreeSpaceEx
	return fmt.Errorf("disk space check not implemented on Windows")
}
