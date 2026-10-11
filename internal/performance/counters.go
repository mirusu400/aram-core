// Package performance owns opt-in host measurements. These counters are never
// guest state. Build with aram_profile to enable timing; ordinary builds remove
// every measurement site through the constant Enabled guard.
package performance

type Phase uint8

const (
	CPU Phase = iota
	GraphicsBlit
	GraphicsPresent
	AudioSMAF
	phaseCount
)

type Counter struct {
	Calls       uint64 `json:"calls"`
	Nanoseconds uint64 `json:"nanoseconds"`
}

// Statistics is process-wide and cumulative. Scopes may nest; their times must
// not be added to infer exclusive frame time. Sampling itself perturbs timing.
type Statistics struct {
	CPU             Counter `json:"cpu"`
	GraphicsBlit    Counter `json:"graphics_blit"`
	GraphicsPresent Counter `json:"graphics_present"`
	AudioSMAF       Counter `json:"audio_smaf"`
}
