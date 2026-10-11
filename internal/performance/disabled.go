//go:build !aram_profile

package performance

import "time"

const Enabled = false

func Start() time.Time        { return time.Time{} }
func Finish(Phase, time.Time) {}
func Snapshot() Statistics    { return Statistics{} }
