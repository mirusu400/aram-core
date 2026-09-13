package skvm

import (
	"context"
	"fmt"
	shared "github.com/mirusu400/aram-core/runtime"
	"strings"
)

const lgtMediaClass = "mmpp/media/MediaPlayer"
const lgtLoopField = "$lgt.media.loop"

// lgtUnsupported is a host capability error, not a guessed handset exception.
func lgtUnsupported(method, reason string) error {
	return fmt.Errorf("unsupported %s.%s: %s", lgtMediaClass, method, reason)
}

func (vm *VM) lgtMedia(receiver uint32) (*Object, *audioClipState, error) {
	o, ok := vm.Object(receiver)
	if !ok {
		return nil, nil, lgtUnsupported("receiver", "missing object")
	}
	clip, ok := o.Native.(*audioClipState)
	if !ok {
		return nil, nil, lgtUnsupported("receiver", "constructor not initialized")
	}
	return o, clip, nil
}

// Source installation is transactional: validate the replacement before retiring
// the previous clip. Unknown codecs are never accepted as silent playback.
func (vm *VM) lgtMediaSource(receiver uint32, data []byte) error {
	_, old, err := vm.lgtMedia(receiver)
	if err != nil {
		return err
	}
	if old.clip != 0 {
		info, e := vm.services.Media.Info(vm.serviceOwner, old.clip)
		if e != nil {
			return e
		}
		if info.State != shared.ClipStopped {
			return lgtUnsupported("setMediaSource", "replacement during playback is unspecified")
		}
	}
	id, err := vm.services.Media.CreateClip(vm.serviceOwner, "", uint64(len(data)))
	if err != nil {
		return lgtUnsupported("setMediaSource", err.Error())
	}
	discard := func() {
		_ = vm.services.Media.DestroyClip(vm.serviceOwner, id, vm.services.Events)
	}
	if _, err = vm.services.Media.Append(vm.serviceOwner, id, data); err != nil {
		discard()
		return lgtUnsupported("setMediaSource", err.Error())
	}
	// Prepare validates deferred decoders without starting playback or
	// invalidating PCM that belongs to another player.
	if err = vm.services.Media.Prepare(vm.serviceOwner, id); err != nil {
		discard()
		return lgtUnsupported("setMediaSource", err.Error())
	}
	if old.clip != 0 {
		if err = vm.services.Media.DestroyClip(vm.serviceOwner, old.clip, vm.services.Events); err != nil {
			discard()
			return err
		}
	}
	old.clip = id
	return nil
}

func (vm *VM) installLGTMediaNatives() {
	vm.RegisterHostClass(lgtMediaClass, "java/lang/Object")
	vm.RegisterNative(lgtMediaClass, "<init>", "()V", func(_ context.Context, vm *VM, r uint32, _ []Value) (Value, bool, error) {
		o, ok := vm.Object(r)
		if !ok {
			return Value{}, false, lgtUnsupported("<init>", "missing receiver")
		}
		if o.Native != nil {
			return Value{}, false, lgtUnsupported("<init>", "reinitialization")
		}
		o.Native = &audioClipState{}
		o.Fields[lgtLoopField] = IntValue(0)
		return Value{}, false, nil
	})
	vm.RegisterNative(lgtMediaClass, "setMediaLocation", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, r uint32, a []Value) (Value, bool, error) {
		name, e := vm.stringArgument(a, 0)
		if e != nil {
			return Value{}, false, e
		}
		data, ok := vm.resource(strings.TrimPrefix(name, "/"))
		if !ok {
			return Value{}, false, vm.newThrowable("java/io/IOException", "media resource not found")
		}
		return Value{}, false, vm.lgtMediaSource(r, data)
	})
	for _, desc := range []string{"([B)V", "([BII)V"} {
		desc := desc
		vm.RegisterNative(lgtMediaClass, "setMediaSource", desc, func(_ context.Context, vm *VM, r uint32, a []Value) (Value, bool, error) {
			ref, e := referenceArgument(a, 0)
			if e != nil {
				return Value{}, false, e
			}
			data, e := vm.ByteArray(ref)
			if e != nil {
				return Value{}, false, lgtUnsupported("setMediaSource", e.Error())
			}
			if desc == "([BII)V" {
				offset, e := intArgument(a, 1)
				if e != nil {
					return Value{}, false, e
				}
				length, e := intArgument(a, 2)
				if e != nil {
					return Value{}, false, e
				}
				if offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(data)) {
					return Value{}, false, lgtUnsupported("setMediaSource", "source bounds out of range; handset exception unspecified")
				}
				data = data[int64(offset) : int64(offset)+int64(length)]
			}
			return Value{}, false, vm.lgtMediaSource(r, data)
		})
	}
	vm.RegisterNative(lgtMediaClass, "isPlayBackLoop", "()Z", func(_ context.Context, vm *VM, r uint32, _ []Value) (Value, bool, error) {
		o, _, e := vm.lgtMedia(r)
		if e != nil {
			return Value{}, false, e
		}
		return o.Fields[lgtLoopField], true, nil
	})
	vm.RegisterNative(lgtMediaClass, "setPlayBackLoop", "(Z)V", func(_ context.Context, vm *VM, r uint32, a []Value) (Value, bool, error) {
		o, c, e := vm.lgtMedia(r)
		if e != nil {
			return Value{}, false, e
		}
		v, e := intArgument(a, 0)
		if e != nil {
			return Value{}, false, e
		}
		if c.clip != 0 {
			info, e := vm.services.Media.Info(vm.serviceOwner, c.clip)
			if e != nil {
				return Value{}, false, e
			}
			if info.State != shared.ClipStopped {
				return Value{}, false, lgtUnsupported("setPlayBackLoop", "changing loop during playback is unspecified")
			}
		}
		if v != 0 {
			v = 1
		}
		o.Fields[lgtLoopField] = IntValue(v)
		return Value{}, false, nil
	})
	for _, name := range []string{"start", "stop", "pause", "resume"} {
		name := name
		vm.RegisterNative(lgtMediaClass, name, "()V", func(_ context.Context, vm *VM, r uint32, _ []Value) (Value, bool, error) {
			o, c, e := vm.lgtMedia(r)
			if e != nil {
				return Value{}, false, e
			}
			if c.clip == 0 {
				// Emulator cleanup identity for a constructed, source-less player.
				// Do not touch shared media or broaden real-clip transitions.
				if name == "stop" {
					return Value{}, false, nil
				}
				return Value{}, false, lgtUnsupported(name, "no source; handset error unspecified")
			}
			switch name {
			case "start":
				loop, _ := o.Fields[lgtLoopField].Int()
				plays := int32(1)
				if loop != 0 {
					plays = -1
				}
				e = vm.services.Media.Play(vm.serviceOwner, c.clip, plays)
			case "stop":
				info, err := vm.services.Media.Info(vm.serviceOwner, c.clip)
				if err != nil {
					return Value{}, false, err
				}
				if info.State == shared.ClipStopped {
					// Idempotent cleanup of a prepared, completed or already
					// stopped clip must not discard another player's queued PCM.
					// Play already rewinds a naturally completed clip on restart.
					return Value{}, false, nil
				}
				e = vm.services.Media.Stop(vm.serviceOwner, c.clip)
				if e == nil {
					e = vm.services.Media.Seek(vm.serviceOwner, c.clip, 0)
				}
			case "pause":
				e = vm.services.Media.Pause(vm.serviceOwner, c.clip)
			case "resume":
				e = vm.services.Media.Resume(vm.serviceOwner, c.clip)
			}
			if e != nil {
				return Value{}, false, lgtUnsupported(name, e.Error()+"; handset error unspecified")
			}
			return Value{}, false, nil
		})
	}
	vm.installLGTVolumeNatives()
}
