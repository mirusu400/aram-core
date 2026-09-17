package application

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/j2me"
)

func TestFactoryLegacySKVMProfileCompatibility(t *testing.T) {
	data := syntheticSKVMPackage(t)
	for _, profile := range []string{"", skvmhost.ProfileID, j2me.ProfileID, j2me.LGTProfileID, "unknown/profile", DefaultProfileID} {
		t.Run("profile="+profile, func(t *testing.T) {
			created, err := NewFactory().Create(context.Background(), machinecore.Source{Name: "synthetic.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data)), ProfileID: profile})
			if profile != "" && profile != skvmhost.ProfileID {
				if err == nil {
					if created != nil {
						_ = created.Close()
					}
					t.Fatalf("legacy SKVM accepted incompatible profile %q", profile)
				}
				if created != nil {
					t.Fatal("incompatible profile returned a machine")
				}
				if !errors.Is(err, ErrUnsupportedSource) || !errors.Is(err, skvmhost.ErrUnsupportedProfile) {
					t.Fatalf("profile error = %v, want ErrUnsupportedSource", err)
				}
				if !strings.Contains(err.Error(), profile) {
					t.Fatalf("error %v omits rejected profile %q", err, profile)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = created.Close() })
			machine, ok := created.(*skvmhost.Machine)
			if !ok {
				t.Fatalf("created %T, want SKVM machine", created)
			}
			info := machine.SourceInfo()
			device := machine.Services().Config.Device
			if info.Format != "skvm" || info.ProfileID != skvmhost.ProfileID || device.ProfileID != skvmhost.ProfileID || device.Carrier != "skt" {
				t.Fatalf("legacy identity: source=%+v device=%+v", info, device)
			}
		})
	}
}
