package ktf

import (
	"context"
)

// ktfNetworkUnavailable is the verdict MC_netConnect's callback carries when
// no carrier data session can be established. WIPI reports a failed connect
// through the callback's error argument, not through the call's return value:
// the call only says the request was accepted.
var ktfNetworkUnavailable = ^uint32(0)

// ktfPendingNetCallback is one queued MC_NET completion. Carrier network
// callbacks share the WIPI-C timer callbacks' serialization discipline, so
// they are queued here and dispatched from activateDueWIPICTimers rather than
// re-entering the guest from inside the host call.
type ktfPendingNetCallback struct {
	procedure uint32
	result    uint32
	parameter uint32
}

// ktfWIPICNetConnect models MC_netConnect(NETCONNECTCB cb, void *param).
//
// ARAM opens no carrier data session for a KTF Clet, and every other slot of
// the WIPI-C network interface is still a no-op, so the honest answer is the
// one a handset out of coverage gives: accept the request and report the
// failure through the callback. Leaving the callback unqueued is what a title
// cannot survive — 미니게임천국4 (issue #56) asks to authenticate on its first
// launch and then waits on that callback forever, painting "접속중..." until
// the user kills it.
func ktfWIPICNetConnect(_ context.Context, runtime *Runtime) (uint32, error) {
	callback, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	parameter, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	runtime.tracef(
		"wipic_net_connect:callback=0x%08x:parameter=0x%08x:result=%d",
		callback,
		parameter,
		int32(ktfNetworkUnavailable),
	)
	if callback == 0 {
		return 0, nil
	}
	runtime.pendingNetCallbacks = append(
		runtime.pendingNetCallbacks,
		ktfPendingNetCallback{
			procedure: callback,
			result:    ktfNetworkUnavailable,
			parameter: parameter,
		},
	)
	return 0, nil
}

// ktfWIPICNetClose models MC_netClose(void). Nothing was opened, so closing
// only has to succeed.
func ktfWIPICNetClose(_ context.Context, runtime *Runtime) (uint32, error) {
	runtime.tracef("wipic_net_close")
	return 0, nil
}
