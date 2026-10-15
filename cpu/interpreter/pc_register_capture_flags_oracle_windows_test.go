//go:build windows && amd64

package interpreter

import "testing"

func TestPCRegisterCaptureFlagsOracleHybridSystemTier(t *testing.T) {
	pcCaptureFlagsOracleSystemTier(t, backendConstructor{"hybrid-jit", NewHybridJIT})
}
