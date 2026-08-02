package corpus

import (
	"strings"
	"testing"
)

func TestCheckpointKeyIsStableAndUnique(t *testing.T) {
	c := Checkpoint{SourceID: "python", SourceCommit: "0123456789abcdef0123456789abcdef01234567", Path: "Doc/library.rst", ContentHash: "ab12"}
	key := c.Key()
	if key != "python@0123456789abcdef0123456789abcdef01234567:Doc/library.rst#ab12" {
		t.Fatalf("checkpoint key = %q", key)
	}
	other := Checkpoint{SourceID: "python", SourceCommit: "0123456789abcdef0123456789abcdef01234567", Path: "Doc/library.rst", ContentHash: "cd34"}
	if other.Key() == key {
		t.Fatal("different content hash must produce a different checkpoint")
	}
}

func TestValidateSourceGateAcceptsPinnedEvidence(t *testing.T) {
	g := SourceGate{
		SourceID:      "python",
		SourceCommit:  "0123456789abcdef0123456789abcdef01234567",
		LicensePath:   "LICENSE",
		LicenseSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		LicenseSPDX:   "PSF-2.0",
	}
	if err := ValidateSourceGate(g); err != nil {
		t.Fatalf("ValidateSourceGate() = %v", err)
	}
}

func TestValidateSourceGateRejectsFloatingCommit(t *testing.T) {
	g := SourceGate{
		SourceID:      "python",
		SourceCommit:  "main",
		LicenseSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		LicenseSPDX:   "PSF-2.0",
	}
	err := ValidateSourceGate(g)
	if err == nil || !strings.Contains(err.Error(), "immutable commit") {
		t.Fatalf("expected immutable commit rejection, got %v", err)
	}
}

func TestValidateSourceGateRejectsAllZeroCommit(t *testing.T) {
	g := SourceGate{
		SourceID:      "python",
		SourceCommit:  strings.Repeat("0", 40),
		LicenseSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		LicenseSPDX:   "PSF-2.0",
	}
	err := ValidateSourceGate(g)
	if err == nil || !strings.Contains(err.Error(), "all-zero placeholder") {
		t.Fatalf("expected placeholder rejection, got %v", err)
	}
}

func TestValidateSourceGateRejectsBadLicenseHash(t *testing.T) {
	g := SourceGate{
		SourceID:      "python",
		SourceCommit:  "0123456789abcdef0123456789abcdef01234567",
		LicenseSHA256: "xyz",
		LicenseSPDX:   "PSF-2.0",
	}
	err := ValidateSourceGate(g)
	if err == nil || !strings.Contains(err.Error(), "64-char hex") {
		t.Fatalf("expected license hash rejection, got %v", err)
	}
}

func TestValidateSourceGateRejectsEmptySPDX(t *testing.T) {
	g := SourceGate{
		SourceID:      "python",
		SourceCommit:  "0123456789abcdef0123456789abcdef01234567",
		LicenseSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	err := ValidateSourceGate(g)
	if err == nil || !strings.Contains(err.Error(), "license_spdx") {
		t.Fatalf("expected spdx rejection, got %v", err)
	}
}

func TestHashBytes(t *testing.T) {
	// sha256("hello") is a well-known value.
	if got := HashBytes([]byte("hello")); got != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("HashBytes(hello) = %s", got)
	}
}
