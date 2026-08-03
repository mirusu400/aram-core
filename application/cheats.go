package application

import (
	"fmt"

	"github.com/mirusu400/aram-core/cheat"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

// AttachCheats wraps an application machine with host-side memory search,
// patch, and freeze support. Callers must use the returned wrapper for all
// subsequent machine operations so guest execution remains serialized with
// memory changes.
func AttachCheats(
	machine machinecore.Machine,
	options cheat.Options,
) (*cheat.Machine, error) {
	applicationMachine, ok := machine.(*Machine)
	if !ok {
		return nil, fmt.Errorf(
			"attach memory cheats: unsupported machine type %T",
			machine,
		)
	}
	return applicationMachine.WithCheats(options)
}

// WithCheats creates a wrapper without changing the core Machine contract or
// exposing the CPU backend to frontends.
func (m *Machine) WithCheats(options cheat.Options) (*cheat.Machine, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, cpu.ErrClosed
	}
	if m.state == machinecore.StateEmpty {
		m.mu.Unlock()
		return nil, fmt.Errorf("attach memory cheats before an application is loaded")
	}
	if options.TargetSHA256 == "" {
		options.TargetSHA256 = m.source.SHA256
	}
	if len(options.Regions) == 0 {
		options.Regions = m.defaultCheatRegionsLocked()
	}
	m.mu.Unlock()

	return cheat.Wrap(m, applicationCheatMemory{machine: m}, options)
}

type applicationCheatMemory struct {
	machine *Machine
}

func (a applicationCheatMemory) ReadMemory(
	address uint32,
	destination []byte,
) error {
	a.machine.mu.Lock()
	defer a.machine.mu.Unlock()
	if a.machine.closed {
		return cpu.ErrClosed
	}
	return a.machine.cpu.ReadMemory(address, destination)
}

func (a applicationCheatMemory) WriteMemory(
	address uint32,
	source []byte,
) error {
	a.machine.mu.Lock()
	defer a.machine.mu.Unlock()
	if a.machine.closed {
		return cpu.ErrClosed
	}
	return a.machine.cpu.WriteMemory(address, source)
}

func (m *Machine) defaultCheatRegionsLocked() []cheat.Region {
	regions := make([]cheat.Region, 0, 8)
	if m.raptor != nil {
		for _, section := range m.raptor.pkg.Image.AllocatedSections() {
			regions = append(regions, cheat.Region{
				Name: fmt.Sprintf(
					"image.raptor.%d.%s",
					section.Index,
					section.Name,
				),
				Start: section.Address,
				Size:  section.Size,
				// mapRaptorImage maps every allocated section read-write, so a
				// hash-keyed code patch may target executable sections the way
				// image.text does for the other loaders. Scanning stays on the
				// writable data sections so an unknown-value scan never walks
				// executable bytes.
				Writable:  true,
				Scannable: section.Writable(),
			})
		}
	} else {
		if m.info.TextSize != 0 {
			regions = append(regions, cheat.Region{
				Name:      "image.text",
				Start:     m.info.TextAddress,
				Size:      m.info.TextSize,
				Writable:  true,
				Scannable: false,
			})
		}
		if m.info.BSSSize != 0 {
			regions = append(regions, cheat.Region{
				Name:      "image.data",
				Start:     m.info.BSSAddress,
				Size:      m.info.BSSSize,
				Writable:  true,
				Scannable: true,
			})
		}
	}
	regions = append(regions, cheat.Region{
		Name:      "wipi.heap",
		Start:     guestHeapBase,
		Size:      guestHeapSize,
		Writable:  true,
		Scannable: true,
	})
	if m.minigame != nil {
		regions = append(regions, cheat.Region{
			Name:      "eads.image-heap",
			Start:     eadsImageHeapBase,
			Size:      eadsImageHeapSize,
			Writable:  true,
			Scannable: false,
		})
	}
	regions = append(regions, cheat.Region{
		Name:      "application.stack",
		Start:     DefaultStackBase,
		Size:      DefaultStackSize,
		Writable:  true,
		Scannable: false,
	})
	return regions
}
