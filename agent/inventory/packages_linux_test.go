//go:build linux

package inventory

import (
	"testing"
)

func TestParseTSV_Normal(t *testing.T) {
	input := "curl\t7.81\nwget\t1.21\n"
	pkgs := parseTSV(input, "apt")
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}
	if pkgs[0].Name != "curl" || pkgs[0].Version != "7.81" || pkgs[0].Manager != "apt" {
		t.Errorf("pkg[0] = %+v", pkgs[0])
	}
	if pkgs[1].Name != "wget" || pkgs[1].Version != "1.21" || pkgs[1].Manager != "apt" {
		t.Errorf("pkg[1] = %+v", pkgs[1])
	}
}

func TestParseTSV_EmptyInput(t *testing.T) {
	pkgs := parseTSV("", "rpm")
	if len(pkgs) != 0 {
		t.Errorf("expected 0 packages, got %d", len(pkgs))
	}
}

func TestParseTSV_SkipsEmptyLines(t *testing.T) {
	input := "curl\t7.81\n\nwget\t1.21\n"
	pkgs := parseTSV(input, "apt")
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}
}

func TestParseTSV_SkipsMissingTab(t *testing.T) {
	input := "curl\t7.81\nnotapackage\nwget\t1.21\n"
	pkgs := parseTSV(input, "apt")
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages (skip line without tab), got %d", len(pkgs))
	}
}

func TestParseTSV_SkipsEmptyName(t *testing.T) {
	input := "\t1.0\ncurl\t7.81\n"
	pkgs := parseTSV(input, "apt")
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package (skip empty name), got %d", len(pkgs))
	}
	if pkgs[0].Name != "curl" {
		t.Errorf("expected curl, got %s", pkgs[0].Name)
	}
}

func TestParseTSV_NoTrailingNewline(t *testing.T) {
	input := "curl\t7.81\nwget\t1.21"
	pkgs := parseTSV(input, "rpm")
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}
}

func TestParseTSV_VersionWithTab(t *testing.T) {
	// SplitN with limit 2 means extra tabs stay in the version field
	input := "curl\t7.81\textra\n"
	pkgs := parseTSV(input, "apt")
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	if pkgs[0].Version != "7.81\textra" {
		t.Errorf("expected version with tab preserved, got %q", pkgs[0].Version)
	}
}
