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
	for reason := StopRequested; reason <= StopExecutionTrap; reason++ {
		if !reason.Valid() {
			t.Fatalf("StopReason(%d).Valid() = false", reason)
		}
	}
	if StopReason(0xff).Valid() {
		t.Fatal("invalid stop reason was accepted")
	}
}

func TestExecutionTrapValidation(t *testing.T) {
	valid := []ExecutionTrap{
		{Address: 0x1000, Mode: ModeARM},
		{Address: 0x1002, Mode: ModeThumb},
	}
	for _, trap := range valid {
		if !trap.Valid() {
			t.Fatalf("ExecutionTrap(%+v).Valid() = false", trap)
		}
	}
	invalid := []ExecutionTrap{
		{Address: 0x1002, Mode: ModeARM},
		{Address: 0x1001, Mode: ModeThumb},
		{Address: 0x1000, Mode: Mode(0xff)},
	}
	for _, trap := range invalid {
		if trap.Valid() {
			t.Fatalf("ExecutionTrap(%+v).Valid() = true", trap)
		}
	}
}
