package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/brew"
	"github.com/mirusu400/aram-core/loader/gnex"
	"github.com/mirusu400/aram-core/loader/j2me"
)

func (f Factory) createJ2MEMachine(ctx context.Context, source machinecore.Source, data []byte) (machinecore.Machine, bool, error) {
	// This fallback follows SKVM, KTF and Raptor. Do not steal existing native,
	// GVM or Android archives merely because they also contain MIDlet metadata.
	if container, err := loader.InspectContainer(data); err == nil && len(container.Images) > 0 {
		return nil, false, nil
	}
	if _, err := gnex.Inspect(data); !errors.Is(err, gnex.ErrNotPackage) {
		return nil, false, nil
	}
	if _, err := brew.Inspect(data); !errors.Is(err, brew.ErrNotPackage) {
		return nil, false, nil
	}
	if _, android := loader.AndroidPackage(data); android {
		return nil, false, nil
	}
	pkg, err := j2me.Inspect(data)
	if errors.Is(err, j2me.ErrNotPackage) {
		return nil, false, nil
	}
	if errors.Is(err, j2me.ErrUnsupportedFeature) {
		return nil, true, fmt.Errorf("%w: inspect J2ME package: %w", ErrUnsupportedSource, err)
	}
	if err != nil {
		return nil, true, fmt.Errorf("inspect J2ME package: %w", err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, digest) {
		return nil, true, fmt.Errorf("load %q: SHA-256 mismatch: expected %s, got %s", source.Name, source.SHA256, digest)
	}
	source.SHA256 = digest
	machine, err := skvmhost.NewJ2ME(ctx, source, pkg, f.FramebufferSize, f.OutputSampleRate, f.OutputChannels)
	return machine, true, err
}
