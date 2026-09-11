package skvm

import (
	"context"
	"strconv"
)

// installLGTVolumeNatives implements the six-step interoperability subset
// documented in docs/lgt-mmpp.md. It changes real shared clip gain, never
// pretends to decode/play a source, and leaves the ambiguous getter unsupported.
func (vm *VM) installLGTVolumeNatives() {
	vm.RegisterNative(lgtMediaClass, "setVolumeLevel", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, r uint32, a []Value) (Value, bool, error) {
		_, clip, err := vm.lgtMedia(r)
		if err != nil {
			return Value{}, false, err
		}
		if clip.clip == 0 {
			return Value{}, false, lgtUnsupported("setVolumeLevel", "no source; handset error unspecified")
		}
		text, err := vm.stringArgument(a, 0)
		if err != nil {
			return Value{}, false, lgtUnsupported("setVolumeLevel", "invalid volume String; handset error unspecified")
		}
		level, err := strconv.ParseInt(text, 10, 32)
		if err != nil || level < 0 || level > 5 {
			return Value{}, false, lgtUnsupported("setVolumeLevel", "only decimal levels 0 through 5 are supported")
		}
		info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
		if err != nil {
			return Value{}, false, err
		}
		err = vm.services.Media.SetClipGain(vm.serviceOwner, clip.clip, uint8(level*20), info.Muted, info.Pan)
		return Value{}, false, err
	})
	vm.RegisterNative(lgtMediaClass, "getVolumeLevel", "()Ljava/lang/String;", func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
		return Value{}, false, lgtUnsupported("getVolumeLevel", "available-level versus current-level String contract is undocumented")
	})
}
