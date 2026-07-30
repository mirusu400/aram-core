package cpu

import "testing"

func TestPermissionsValidation(t *testing.T) {
	for _, permissions := range []Permissions{
		PermissionRead,
		PermissionWrite,
		PermissionExecute,
		PermissionRead | PermissionWrite | PermissionExecute,
	} {
		if !permissions.Valid() {
			t.Fatalf("Permissions(%#x).Valid() = false", permissions)
		}
	}
	if Permissions(0).Valid() || Permissions(0x80).Valid() {
		t.Fatal("invalid permissions were accepted")
	}
}

func TestIdentityValidation(t *testing.T) {
	identity := Identity{
		Name:         "portable",
		Version:      "1",
		Architecture: ARMv5TE,
	}
	if err := identity.Validate(); err != nil {
		t.Fatal(err)
	}
	identity.Version = ""
	if err := identity.Validate(); err == nil {
		t.Fatal("Identity.Validate accepted an empty version")
	}
}

func TestStopReasonValidation(t *testing.T) {
	for reason := StopRequested; reason <= StopExited; reason++ {
		if !reason.Valid() {
			t.Fatalf("StopReason(%d).Valid() = false", reason)
		}
	}
	if StopReason(0xff).Valid() {
		t.Fatal("invalid stop reason was accepted")
	}
}
