package application

import (
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/gnex32"
)

func TestGNEX32CompatibilityRegistrySelectsHashProfile(t *testing.T) {
	second := gnex32.DeviceProfile{
		ID: "gnex32/test/second", ScreenWidth: 96, ScreenHeight: 128,
		Identity: gnex32.Identity{MIN: "0100000000", UserID: "SECOND"},
	}
	registry, err := newGNEX32CompatibilityRegistry([]gnex32CompatibilityProfile{
		{SHA256: GNEX32VirusSHA256, Title: "first", Device: gnex32VirusProfile},
		{SHA256: strings.Repeat("a", 64), Title: "second", Device: second},
	})
	if err != nil {
		t.Fatal(err)
	}
	for digest, wantID := range map[string]string{
		strings.ToUpper(GNEX32VirusSHA256): GNEX32VirusProfileID,
		strings.Repeat("A", 64):            second.ID,
	} {
		profile, ok := registry.Lookup(digest)
		if !ok || profile.Device.ID != wantID {
			t.Fatalf("lookup %q = %+v, %v", digest, profile, ok)
		}
	}
	if _, ok := registry.Lookup(strings.Repeat("b", 64)); ok {
		t.Fatal("unregistered digest was qualified")
	}
}

func TestGNEX32CompatibilityRegistryRejectsAmbiguity(t *testing.T) {
	valid := gnex32CompatibilityProfile{
		SHA256: strings.Repeat("a", 64), Title: "title", Device: gnex32VirusProfile,
	}
	badProfile := gnex32VirusProfile
	badProfile.ScreenWidth = 0
	for name, entries := range map[string][]gnex32CompatibilityProfile{
		"bad digest":        {{SHA256: "AA", Title: "title", Device: gnex32VirusProfile}},
		"noncanonical hash": {{SHA256: strings.Repeat("A", 64), Title: "title", Device: gnex32VirusProfile}},
		"empty title":       {{SHA256: strings.Repeat("b", 64), Device: gnex32VirusProfile}},
		"bad device":        {{SHA256: strings.Repeat("b", 64), Title: "title", Device: badProfile}},
		"duplicate digest":  {valid, valid},
		"duplicate profile": {
			valid,
			{SHA256: strings.Repeat("b", 64), Title: "other", Device: gnex32VirusProfile},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newGNEX32CompatibilityRegistry(entries); err == nil {
				t.Fatal("invalid compatibility registry accepted")
			}
		})
	}
}

func TestProductionGNEX32CompatibilityRegistryOwnsVirusProfile(t *testing.T) {
	profile, ok := gnex32CompatibilityProfiles.Lookup(GNEX32VirusSHA256)
	if !ok || profile.Title != "바이러스무삭제" || profile.Device != gnex32VirusProfile {
		t.Fatalf("Virus compatibility profile = %+v, %v", profile, ok)
	}
}
