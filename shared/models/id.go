package models

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// Prefixed ID format: "prefix_" + 16 random hex chars (8 bytes).
// Examples: ten_a1b2c3d4e5f6a7b8, dev_0011223344556677

func newID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(b)
}

func NewTenantID() string          { return newID("ten") }
func NewUserID() string            { return newID("usr") }
func NewRoleID() string            { return newID("rol") }
func NewAPIKeyID() string          { return newID("key") }
func NewDeviceID() string          { return newID("dev") }
func NewGroupID() string           { return newID("grp") }
func NewTagID() string             { return newID("tag") }
func NewSiteID() string            { return newID("sit") }
func NewJobID() string             { return newID("job") }
func NewJobResultID() string       { return newID("jrs") }
func NewScheduledJobID() string    { return newID("sjb") }
func NewAuditEntryID() string      { return newID("aud") }
func NewAlertRuleID() string       { return newID("alr") }
func NewCertificateID() string     { return newID("crt") }
func NewEnrollmentTokenID() string { return newID("enr") }
func NewSigningKeyID() string      { return newID("sgk") }
func NewFileID() string            { return newID("fil") }
func NewUploadID() string          { return newID("upl") }
func NewAgentVersionID() string    { return newID("avg") }
func NewInstallerID() string       { return newID("ins") }
func NewInventoryHWID() string     { return newID("ihw") }
func NewInventoryPkgID() string    { return newID("ipk") }
func NewDeviceLogID() string       { return newID("dlg") }

// ValidPrefix checks if an ID starts with the expected prefix.
func ValidPrefix(id, prefix string) bool {
	return strings.HasPrefix(id, prefix+"_")
}
