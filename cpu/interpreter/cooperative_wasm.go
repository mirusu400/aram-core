//go:build js && wasm

package interpreter

// WASM has no asynchronous Go goroutine preemption. Yield between bounded
// execution batches so a runnable Stop/cancellation goroutine can execute.
// This schedules Go work only; it does not sleep or change the guest clock.
const cooperativeRunScheduler = true

// WASM benefits from avoiding the non-inlineable translation helper on cache
// hits. Native/Android measurements did not justify changing their dispatcher.
const inlineThumbDispatchCacheLookup = true
