package wipi

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	"time"
)

func (r *Runtime) dispatchMisc(name string) (guest.WIPIReturn, bool, error) {
	switch name {
	case "MC_miscBackLight":
		args, err := r.args(4)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		for index, value := range args {
			r.backlight[index] = int32(value)
		}
		on := r.backlight[1] != 0 || r.backlight[2] != 0
		duration := time.Duration(max(0, r.backlight[3])) * time.Millisecond
		if err := r.Services.Device.SetBacklight(
			on,
			duration,
			r.Services.Clock.Monotonic(),
		); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_miscSetLed":
		value, err := r.arg(0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		r.ledState = int32(value)
		if err := r.Services.Device.SetLED(0, r.ledState); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_miscGetLed":
		return guest.WIPIReturn{Low: uint32(r.ledState)}, true, nil
	case "MC_miscGetLedCount":
		return guest.WIPIReturn{Low: 1}, true, nil
	default:
		return guest.WIPIReturn{}, false, nil
	}
}
