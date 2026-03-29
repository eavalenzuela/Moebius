//go:build linux

package inventory

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moebius-oss/moebius/shared/models"
)

func collectHardware() (*models.HardwareInventory, error) {
	hw := &models.HardwareInventory{}

	if cpu, err := readCPUInfo(); err == nil {
		hw.CPU = cpu
	}
	if ram, err := readMemInfo(); err == nil {
		hw.RAMMB = ram
	}
	if disks, err := readDisks(); err == nil {
		hw.Disks = disks
	}
	if nics, err := readNetworkInterfaces(); err == nil {
		hw.NetworkInterfaces = nics
	}

	return hw, nil
}

func readCPUInfo() (*models.CPUInfo, error) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info := &models.CPUInfo{}
	coreSet := make(map[string]bool)
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "model name":
			if info.Model == "" {
				info.Model = val
			}
		case "core id":
			coreSet[val] = true
		case "processor":
			info.Threads++
		}
	}

	info.Cores = len(coreSet)
	if info.Cores == 0 {
		info.Cores = info.Threads // single-core or VMs without core id
	}

	return info, scanner.Err()
}

func readMemInfo() (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return 0, err
				}
				return kb / 1024, nil
			}
		}
	}
	return 0, fmt.Errorf("MemTotal not found")
}

func readDisks() ([]models.DiskInfo, error) {
	const sysBlock = "/sys/block"
	entries, err := os.ReadDir(sysBlock)
	if err != nil {
		return nil, err
	}

	var disks []models.DiskInfo
	for _, entry := range entries {
		name := entry.Name()
		// Skip virtual devices
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}

		blockDir := filepath.Join(sysBlock, name)
		sizePath := filepath.Join(blockDir, "size")
		sizeData, err := os.ReadFile(sizePath) //nolint:gosec // reading known sysfs path
		if err != nil {
			continue
		}
		sectors, err := strconv.ParseInt(strings.TrimSpace(string(sizeData)), 10, 64)
		if err != nil || sectors == 0 {
			continue
		}

		disk := models.DiskInfo{
			Device:    "/dev/" + name,
			SizeBytes: sectors * 512,
		}

		// Detect type from rotational flag
		rotPath := filepath.Join(blockDir, "queue", "rotational")
		if rotData, err := os.ReadFile(rotPath); err == nil { //nolint:gosec // reading known sysfs path
			if strings.TrimSpace(string(rotData)) == "0" {
				disk.Type = "ssd"
			} else {
				disk.Type = "hdd"
			}
		}

		disks = append(disks, disk)
	}

	return disks, nil
}

func readNetworkInterfaces() ([]models.NetworkIfInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var result []models.NetworkIfInfo
	for _, iface := range ifaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		ni := models.NetworkIfInfo{
			Name: iface.Name,
			MAC:  iface.HardwareAddr.String(),
		}

		addrs, err := iface.Addrs()
		if err == nil {
			for _, addr := range addrs {
				ni.IPs = append(ni.IPs, addr.String())
			}
		}

		result = append(result, ni)
	}

	return result, nil
}
