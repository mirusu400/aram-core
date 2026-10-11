package core

import "github.com/mirusu400/aram-core/internal/performance"

// PerformanceStatistics contains host-only, process-wide cumulative scopes.
// Times are inclusive, may overlap and are not part of deterministic state.
// Only builds with aram_profile collect them. Timing evidence should come from
// a separate ordinary build; diagnostic clock reads add their own overhead.
type PerformanceStatistics = performance.Statistics

func PerformanceInstrumentationEnabled() bool { return performance.Enabled }

func ReadPerformanceStatistics() PerformanceStatistics { return performance.Snapshot() }
