package application

import (
	"fmt"

	"github.com/mirusu400/aram-core/loader"
)

// UnsupportedPlatformError identifies a validated legacy container whose
// execution runtime is unavailable. It does not indicate that an executable
// image loaded or that any guest instruction ran.
//
// ProfileID versions the recognition identity, not a guest SDK or state schema.
// Frontends may display it, but must not enable execution or state capabilities.
type UnsupportedPlatformError struct {
	Kind      loader.Kind
	ProfileID string
	Reason    string
}

func (e *UnsupportedPlatformError) Error() string {
	return fmt.Sprintf("%v: %s", ErrUnsupportedSource, e.Reason)
}

func (e *UnsupportedPlatformError) Unwrap() error { return ErrUnsupportedSource }
