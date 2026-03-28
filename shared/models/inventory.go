package models

import "time"

type HardwareInventory struct {
	ID                string          `json:"id"`
	DeviceID          string          `json:"device_id"`
	CollectedAt       time.Time       `json:"collected_at"`
	CPU               *CPUInfo        `json:"cpu,omitempty"`
	RAMMB             int64           `json:"ram_mb,omitempty"`
	Disks             []DiskInfo      `json:"disks,omitempty"`
	NetworkInterfaces []NetworkIfInfo `json:"network_interfaces,omitempty"`
}

type CPUInfo struct {
	Model   string `json:"model"`
	Cores   int    `json:"cores"`
	Threads int    `json:"threads"`
}

type DiskInfo struct {
	Device     string `json:"device"`
	SizeBytes  int64  `json:"size_bytes"`
	Type       string `json:"type,omitempty"` // "ssd", "hdd", etc.
	MountPoint string `json:"mount_point,omitempty"`
}

type NetworkIfInfo struct {
	Name string   `json:"name"`
	MAC  string   `json:"mac"`
	IPs  []string `json:"ips,omitempty"`
}

type Package struct {
	ID          string     `json:"id"`
	DeviceID    string     `json:"device_id"`
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Manager     string     `json:"manager"` // "apt", "dnf", "msi", etc.
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
}
