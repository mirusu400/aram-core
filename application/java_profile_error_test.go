package application

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
)

func TestFactoryJavaProfileErrorClassification(t *testing.T) {
	data := syntheticSKVMZIP(t, syntheticJ2MEFiles(t))
	for _, profile := range []string{skvmhost.ProfileID, "unknown/profile"} {
		t.Run(profile, func(t *testing.T) {
			created, err := NewFactory().Create(context.Background(), machinecore.Source{Name: "synthetic.jar", ReaderAt: bytes.NewReader(data), Size: int64(len(data)), ProfileID: profile})
			if created != nil {
				t.Fatal("incompatible profile returned a machine")
			}
			if !errors.Is(err, ErrUnsupportedSource) || !errors.Is(err, skvmhost.ErrUnsupportedProfile) {
				t.Fatalf("profile error = %v, want ErrUnsupportedSource", err)
			}
		})
	}
	files := syntheticJ2MEFiles(t)
	files["Game.class"] = []byte("malformed")
	data = syntheticSKVMZIP(t, files)
	_, err := NewFactory().Create(context.Background(), machinecore.Source{Name: "malformed.jar", ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
	if err == nil || errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("malformed class error = %v, must remain malformed", err)
	}
}
