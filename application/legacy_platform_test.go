package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestFactoryNamesStandaloneGNEXWithoutClaimingExecution(t *testing.T) {
	data := syntheticGNEXSGS()
	machine, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: "synthetic.sgs", ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if machine != nil {
		machine.Close()
		t.Fatal("recognized-only GVM unexpectedly created an executable machine")
	}
	if !errors.Is(err, ErrUnsupportedSource) || !strings.Contains(err.Error(), "GVM") || !strings.Contains(err.Error(), "does not yet execute") {
		t.Fatalf("standalone GVM must report precise unsupported execution: %v", err)
	}
	var unsupported *UnsupportedPlatformError
	if !errors.As(err, &unsupported) || unsupported.Kind != loader.KindGNEX || unsupported.ProfileID != "gvm-container-v1/skt/generic" {
		t.Fatalf("standalone GVM must preserve independent recognition identity: %v", err)
	}
}

func syntheticBREWMIF() []byte {
	data := make([]byte, 64)
	for offset, value := range map[int]uint32{0: 0x10011, 8: 32, 12: 8, 16: 40, 20: 1, 24: 48, 28: 16} {
		binary.LittleEndian.PutUint32(data[offset:], value)
	}
	return data
}

func TestFactoryRecognizesBREWWithoutClaimingExecution(t *testing.T) {
	data := testZIP(t, map[string][]byte{
		"metadata.mif":       syntheticBREWMIF(),
		"different-name.mod": []byte("synthetic opaque module, not ARM code"),
	})
	machine, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: "synthetic.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if machine != nil {
		machine.Close()
		t.Fatal("recognized-only BREW unexpectedly created an executable machine")
	}
	var unsupported *UnsupportedPlatformError
	if !errors.Is(err, ErrUnsupportedSource) || !errors.As(err, &unsupported) {
		t.Fatalf("BREW requires typed unsupported execution: %v", err)
	}
	if unsupported.Kind != loader.KindBREW || unsupported.ProfileID != "brew-container-v1/unknown/generic" || !strings.Contains(err.Error(), "does not yet execute BREW modules") {
		t.Fatalf("incorrect recognition identity: %#v", unsupported)
	}
}

func TestFactoryDoesNotRecognizeMalformedBREW(t *testing.T) {
	data := testZIP(t, map[string][]byte{
		"metadata.mif": []byte("not a MIF"), "app.mod": []byte("opaque"),
	})
	_, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: "synthetic.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	var unsupported *UnsupportedPlatformError
	if err == nil || errors.As(err, &unsupported) {
		t.Fatalf("malformed BREW must not receive validated recognition identity: %v", err)
	}
}

func TestFactoryPreservesMalformedGNEXDiagnosticAndAndroidPriority(t *testing.T) {
	for _, android := range []bool{false, true} {
		files := map[string][]byte{"sample.sgs": []byte("broken"), "sample.inf": []byte("descriptor")}
		if android {
			files["AndroidManifest.xml"] = []byte("synthetic xml")
			files["classes.dex"] = []byte("dex 035 synthetic")
		}
		data := testZIP(t, files)
		_, err := NewFactory().Create(context.Background(), machinecore.Source{
			Name: "synthetic.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
		})
		if android {
			if !errors.Is(err, ErrUnsupportedSource) || !strings.Contains(err.Error(), "Android") {
				t.Fatalf("malformed SGS must not hide Android classification: %v", err)
			}
		} else {
			var formatErr *gnex.FormatError
			if !errors.As(err, &formatErr) || formatErr.Path != "sample.sgs" {
				t.Fatalf("malformed GNEX must preserve its diagnostic: %v", err)
			}
		}
	}
}
