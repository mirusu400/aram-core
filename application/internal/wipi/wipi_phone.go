package wipi

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) dispatchPhone(name string) (guest.WIPIReturn, bool, error) {
	if name != "MC_phnCallPlace" {
		return guest.WIPIReturn{}, false, nil
	}
	address, err := r.arg(0)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	number, err := r.ReadCString(address)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	r.phoneRequests = append(r.phoneRequests, append([]byte(nil), number...))
	if _, err := r.Services.Device.Request(
		r.ServiceOwner,
		shared.RequestPhone,
		string(number),
		nil,
		r.Services.Clock.Monotonic(),
	); err != nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	return guest.WIPIReturn{}, true, nil
}
