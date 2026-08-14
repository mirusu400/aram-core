package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/ktf"
	"github.com/mirusu400/aram-core/loader/raptor"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
)

func (f Factory) createSKVMMachine(
	ctx context.Context,
	source machinecore.Source,
) (machinecore.Machine, bool, error) {
	if err := source.Validate(); err != nil {
		return nil, false, nil
	}
	if source.Size > maxApplicationSize {
		return nil, false, nil
	}
	data, err := io.ReadAll(io.NewSectionReader(source.ReaderAt, 0, source.Size))
	if err != nil || int64(len(data)) != source.Size {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, true, fmt.Errorf("read SKVM application: %w", err)
	}
	// Preserve the established native-package precedence. Exact KTF or Raptor
	// packages continue through Machine.Load; only an unclaimed ZIP reaches
	// the SKVM probe.
	if _, inspectErr := ktf.Inspect(data); inspectErr == nil ||
		nativeKTFDiagnostic(inspectErr) {
		return nil, false, nil
	}
	if _, inspectErr := raptor.Inspect(data); inspectErr == nil ||
		nativeRaptorDiagnostic(inspectErr) {
		return nil, false, nil
	}
	pkg, err := skloader.Inspect(data)
	if errors.Is(err, skloader.ErrNotPackage) {
		return nil, false, nil
	}
	var formatErr *skloader.FormatError
	if errors.As(err, &formatErr) &&
		formatErr.Path == "archive" &&
		strings.HasPrefix(formatErr.Reason, "invalid ZIP:") {
		// A corrupt outer ZIP has not supplied enough evidence to claim the
		// package as SKVM. Preserve the established native/generic probing
		// result so an ordinary or unsupported Java archive is not
		// misclassified as a malformed SKVM distribution.
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("inspect SKVM package: %w", err)
	}
	digest := sha256.Sum256(data)
	actualSHA256 := hex.EncodeToString(digest[:])
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, actualSHA256) {
		return nil, true, fmt.Errorf(
			"load %q: SHA-256 mismatch: expected %s, got %s",
			source.Name,
			source.SHA256,
			actualSHA256,
		)
	}
	source.SHA256 = actualSHA256
	machine, err := skvmhost.New(ctx, source, pkg, f.FramebufferSize)
	return machine, true, err
}

func nativeKTFDiagnostic(err error) bool {
	if err == nil || errors.Is(err, ktf.ErrNotPackage) {
		return false
	}
	var formatErr *ktf.FormatError
	return !errors.As(err, &formatErr) ||
		formatErr.Path != "archive" ||
		!strings.HasPrefix(formatErr.Reason, "invalid ZIP:")
}

func nativeRaptorDiagnostic(err error) bool {
	if err == nil || errors.Is(err, raptor.ErrNotPackage) {
		return false
	}
	var formatErr *raptor.FormatError
	return !errors.As(err, &formatErr) ||
		formatErr.Path != "archive" ||
		!strings.HasPrefix(formatErr.Reason, "invalid ZIP:")
}
