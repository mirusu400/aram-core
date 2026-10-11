//go:build aram_profile

package performance

import (
	"sync/atomic"
	"time"
)

const Enabled = true

var phases [phaseCount]struct {
	calls atomic.Uint64
	ns    atomic.Uint64
}

func Start() time.Time { return time.Now() }

func Finish(phase Phase, started time.Time) {
	elapsed := time.Since(started)
	if phase >= phaseCount || elapsed < 0 {
		return
	}
	phases[phase].ns.Add(uint64(elapsed))
	phases[phase].calls.Add(1)
}

func Snapshot() Statistics {
	read := func(phase Phase) Counter {
		return Counter{Calls: phases[phase].calls.Load(), Nanoseconds: phases[phase].ns.Load()}
	}
	return Statistics{
		CPU: read(CPU), GraphicsBlit: read(GraphicsBlit),
		GraphicsPresent: read(GraphicsPresent), AudioSMAF: read(AudioSMAF),
	}
}
