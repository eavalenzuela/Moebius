//go:build windows

package inventory

import "github.com/moebius-oss/moebius/shared/models"

func collectHardware() (*models.HardwareInventory, error) {
	// TODO: implement Windows hardware collection via WMI
	return &models.HardwareInventory{}, nil
}
