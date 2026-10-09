package e2e_test

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProductionLocalCoreIdentityHasPrivateWindowsACL(t *testing.T) {
	fixture := startProductionLocalCore(t)
	descriptor, err := windows.GetNamedSecurityInfo(filepath.Join(fixture.dir, "identity", "identity.sqlite"), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("local credential store inherits unrelated Windows directory grants")
	}
}
