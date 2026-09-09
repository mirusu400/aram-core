package skvm

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"path"
	"strings"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	playerStateField     = "$media.state"
	playerTypeField      = "$media.contentType"
	playerLoopField      = "$media.loopCount"
	playerListenersField = "$media.listeners"
	playerVolumeField    = "$media.volumeControl"
	playerToneField      = "$media.toneControl"
	controlPlayerField   = "$media.player"
	playerClosed         = int32(0)
	playerUnrealized     = int32(100)
	playerRealized       = int32(200)
	playerPrefetched     = int32(300)
	playerStarted        = int32(400)
)

func (vm *VM) installMIDPMediaNatives() {
	vm.installMediaManagerNatives()
	vm.installPlayerNatives()
	vm.installMediaControlNatives()
	vm.RegisterNative("javax/microedition/media/MediaException", "<init>", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/media/MediaException", "<init>", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		message, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, message)
	})
	vm.installMediaStaticFields()
}

func (vm *VM) installMediaManagerNatives() {
	vm.RegisterNative("javax/microedition/media/Manager", "createPlayer", "(Ljava/io/InputStream;Ljava/lang/String;)Ljavax/microedition/media/Player;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		streamReference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if streamReference == 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		mediaType, err := vm.stringArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		stream, err := vm.inputStream(streamReference)
		if err != nil {
			return Value{}, false, err
		}
		data := append([]byte(nil), stream.data[stream.offset:]...)
		stream.offset = len(stream.data)
		return vm.newMIDPPlayer(mediaType, data)
	})
	vm.RegisterNative("javax/microedition/media/Manager", "createPlayer", "(Ljava/lang/String;)Ljavax/microedition/media/Player;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		locator, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if locator == "device://tone" {
			return vm.newMIDPPlayer("audio/x-tone-seq", nil)
		}
		name := locator
		for _, prefix := range []string{"resource://", "resource:/", "jar://", "jar:/"} {
			if strings.HasPrefix(name, prefix) {
				name = strings.TrimPrefix(name, prefix)
				break
			}
		}
		name = strings.TrimPrefix(name, "/")
		data, ok := vm.resource(name)
		if !ok {
			return Value{}, false, vm.newThrowable("java/io/IOException", "media resource not found")
		}
		return vm.newMIDPPlayer(strings.TrimPrefix(strings.ToLower(path.Ext(name)), "."), data)
	})
	vm.RegisterNative("javax/microedition/media/Manager", "getSupportedContentTypes", "(Ljava/lang/String;)[Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return ReferenceValue(vm.mediaStringArray([]string{"audio/midi", "audio/sp-midi", "audio/x-smaf", "audio/x-tone-seq", "audio/wav"})), true, nil
	})
	vm.RegisterNative("javax/microedition/media/Manager", "getSupportedProtocols", "(Ljava/lang/String;)[Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return ReferenceValue(vm.mediaStringArray([]string{"device", "resource"})), true, nil
	})
	vm.RegisterNative("javax/microedition/media/Manager", "playTone", "(III)V", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		note, _ := intArgument(args, 0)
		duration, _ := intArgument(args, 1)
		volume, _ := intArgument(args, 2)
		if note < 0 || note > 127 || duration <= 0 {
			return Value{}, false, vm.newThrowable("javax/microedition/media/MediaException", "invalid tone")
		}
		return Value{}, false, vm.playManagerTone(note, duration, max(0, min(100, volume)))
	})
}

func (vm *VM) mediaStringArray(input []string) uint32 {
	values := make([]Value, len(input))
	for index, value := range input {
		values[index] = ReferenceValue(vm.NewString(value))
	}
	return vm.newArray("[Ljava/lang/String;", values)
}

func (vm *VM) newMIDPPlayer(mediaType string, data []byte) (Value, bool, error) {
	clip, err := vm.services.Media.CreateClip(vm.serviceOwner, mediaType, uint64(len(data)))
	if err != nil {
		return Value{}, false, vm.newThrowable("javax/microedition/media/MediaException", err.Error())
	}
	if len(data) != 0 {
		if _, err = vm.services.Media.Append(vm.serviceOwner, clip, data); err != nil {
			_ = vm.services.Media.DestroyClip(vm.serviceOwner, clip, vm.services.Events)
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
	}
	reference := vm.NewObject("javax/microedition/media/Player", &audioClipState{clip: clip})
	object, _ := vm.Object(reference)
	object.Fields[playerStateField] = IntValue(playerUnrealized)
	object.Fields[playerTypeField] = ReferenceValue(vm.NewString(mediaType))
	object.Fields[playerLoopField] = IntValue(1)
	object.Fields[playerListenersField] = ReferenceValue(vm.newArray("[Ljavax/microedition/media/PlayerListener;", nil))
	return ReferenceValue(reference), true, nil
}

func (vm *VM) player(reference uint32) (*Object, *audioClipState, error) {
	object, ok := vm.Object(reference)
	if !ok || object.Class != "javax/microedition/media/Player" {
		return nil, nil, fmt.Errorf("invalid Player reference")
	}
	clip, ok := object.Native.(*audioClipState)
	if !ok {
		return nil, nil, fmt.Errorf("Player has invalid native state")
	}
	return object, clip, nil
}

func (vm *VM) requireOpenPlayer(reference uint32) (*Object, *audioClipState, int32, error) {
	object, clip, err := vm.player(reference)
	if err != nil {
		return nil, nil, 0, err
	}
	state, _ := object.Fields[playerStateField].Int()
	if state == playerClosed {
		return nil, nil, 0, vm.newThrowable("java/lang/IllegalStateException", "Player is closed")
	}
	return object, clip, state, nil
}

func (vm *VM) installPlayerNatives() {
	for _, transition := range []struct {
		name  string
		state int32
	}{{"realize", playerRealized}, {"prefetch", playerPrefetched}} {
		transition := transition
		vm.RegisterNative("javax/microedition/media/Player", transition.name, "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, _, state, err := vm.requireOpenPlayer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if state < transition.state {
				object.Fields[playerStateField] = IntValue(transition.state)
			}
			return Value{}, false, nil
		})
	}
	vm.RegisterNative("javax/microedition/media/Player", "start", "()V", func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, clip, state, err := vm.requireOpenPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if state == playerStarted {
			return Value{}, false, nil
		}
		loop, _ := object.Fields[playerLoopField].Int()
		if err := vm.services.Media.Play(vm.serviceOwner, clip.clip, loop); err != nil {
			return Value{}, false, vm.newThrowable("javax/microedition/media/MediaException", err.Error())
		}
		object.Fields[playerStateField] = IntValue(playerStarted)
		info, _ := vm.services.Media.Info(vm.serviceOwner, clip.clip)
		data := ReferenceValue(vm.newWrapper("java/lang/Long", LongValue(info.Position.Microseconds())))
		return Value{}, false, vm.notifyPlayerListeners(ctx, receiver, "started", data)
	})
	vm.RegisterNative("javax/microedition/media/Player", "stop", "()V", func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, clip, state, err := vm.requireOpenPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if state == playerStarted {
			if err := vm.services.Media.Stop(vm.serviceOwner, clip.clip); err != nil {
				return Value{}, false, vm.newThrowable("javax/microedition/media/MediaException", err.Error())
			}
			object.Fields[playerStateField] = IntValue(playerPrefetched)
			info, _ := vm.services.Media.Info(vm.serviceOwner, clip.clip)
			data := ReferenceValue(vm.newWrapper("java/lang/Long", LongValue(info.Position.Microseconds())))
			if err := vm.notifyPlayerListeners(ctx, receiver, "stopped", data); err != nil {
				return Value{}, false, err
			}
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/media/Player", "deallocate", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, clip, state, err := vm.requireOpenPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if state == playerStarted {
			_ = vm.services.Media.Stop(vm.serviceOwner, clip.clip)
		}
		object.Fields[playerStateField] = IntValue(playerRealized)
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/media/Player", "close", "()V", func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, clip, err := vm.player(receiver)
		if err != nil {
			return Value{}, false, err
		}
		state, _ := object.Fields[playerStateField].Int()
		if state == playerClosed {
			return Value{}, false, nil
		}
		if clip.clip != 0 {
			if info, infoErr := vm.services.Media.Info(vm.serviceOwner, clip.clip); infoErr == nil && info.State != shared.ClipStopped {
				_ = vm.services.Media.Stop(vm.serviceOwner, clip.clip)
			}
			if err := vm.services.Media.DestroyClip(vm.serviceOwner, clip.clip, vm.services.Events); err != nil {
				return Value{}, false, err
			}
			clip.clip = 0
		}
		object.Fields[playerStateField] = IntValue(playerClosed)
		return Value{}, false, vm.notifyPlayerListeners(ctx, receiver, "closed", ReferenceValue(0))
	})
	vm.RegisterNative("javax/microedition/media/Player", "getState", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, _, err := vm.player(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return object.Fields[playerStateField], true, nil
	})
	vm.RegisterNative("javax/microedition/media/Player", "getContentType", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, _, state, err := vm.requireOpenPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if state == playerUnrealized {
			return Value{}, false, vm.newThrowable("java/lang/IllegalStateException", "Player is unrealized")
		}
		return object.Fields[playerTypeField], true, nil
	})
	for _, method := range []struct {
		name     string
		duration bool
	}{{"getMediaTime", false}, {"getDuration", true}} {
		method := method
		vm.RegisterNative("javax/microedition/media/Player", method.name, "()J", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			_, clip, _, err := vm.requireOpenPlayer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
			if err != nil {
				return LongValue(-1), true, nil
			}
			value := info.Position
			if method.duration {
				value = info.Duration
			}
			if method.duration && value <= 0 {
				return LongValue(-1), true, nil
			}
			return LongValue(value.Microseconds()), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/media/Player", "setMediaTime", "(J)J", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		_, clip, _, err := vm.requireOpenPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		position, err := args[0].Long()
		if err != nil {
			return Value{}, false, err
		}
		position = max(0, position)
		if err := vm.services.Media.Seek(vm.serviceOwner, clip.clip, time.Duration(position)*time.Microsecond); err != nil {
			return Value{}, false, vm.newThrowable("javax/microedition/media/MediaException", err.Error())
		}
		info, _ := vm.services.Media.Info(vm.serviceOwner, clip.clip)
		return LongValue(info.Position.Microseconds()), true, nil
	})
	vm.RegisterNative("javax/microedition/media/Player", "setLoopCount", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		object, _, state, err := vm.requireOpenPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		count, _ := intArgument(args, 0)
		if state == playerStarted {
			return Value{}, false, vm.newThrowable("java/lang/IllegalStateException", "Player is started")
		}
		if count == 0 || count < -1 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		object.Fields[playerLoopField] = IntValue(count)
		return Value{}, false, nil
	})
	vm.installPlayerListenerNatives()
	vm.RegisterNative("javax/microedition/media/Player", "getControl", "(Ljava/lang/String;)Ljavax/microedition/media/Control;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return vm.playerControl(receiver, args)
	})
	vm.RegisterNative("javax/microedition/media/Player", "getControls", "()[Ljavax/microedition/media/Control;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if _, _, _, err := vm.requireOpenPlayer(receiver); err != nil {
			return Value{}, false, err
		}
		volume, tone := vm.ensurePlayerControl(receiver, false), vm.ensurePlayerControl(receiver, true)
		return ReferenceValue(vm.newArray("[Ljavax/microedition/media/Control;", []Value{ReferenceValue(volume), ReferenceValue(tone)})), true, nil
	})
}

func (vm *VM) installPlayerListenerNatives() {
	for _, name := range []string{"addPlayerListener", "removePlayerListener"} {
		name := name
		vm.RegisterNative("javax/microedition/media/Player", name, "(Ljavax/microedition/media/PlayerListener;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			if _, _, _, err := vm.requireOpenPlayer(receiver); err != nil {
				return Value{}, false, err
			}
			listener, _ := referenceArgument(args, 0)
			if listener == 0 {
				return Value{}, false, nil
			}
			listeners, _ := vm.objectArray(receiver, playerListenersField, "[Ljavax/microedition/media/PlayerListener;")
			for index, value := range listeners.Elements {
				current, _ := value.Reference()
				if current == listener {
					if name == "removePlayerListener" {
						listeners.Elements = append(listeners.Elements[:index], listeners.Elements[index+1:]...)
					}
					return Value{}, false, nil
				}
			}
			if name == "addPlayerListener" {
				listeners.Elements = append(listeners.Elements, ReferenceValue(listener))
			}
			return Value{}, false, nil
		})
	}
}

func (vm *VM) notifyPlayerListeners(ctx context.Context, player uint32, event string, data Value) error {
	object, _, err := vm.player(player)
	if err != nil {
		return err
	}
	listenersValue, ok := object.Fields[playerListenersField]
	if !ok {
		return nil
	}
	listenersReference, _ := listenersValue.Reference()
	listenersObject, _ := vm.Object(listenersReference)
	if listenersObject == nil || listenersObject.Array == nil {
		return nil
	}
	for _, listenerValue := range append([]Value(nil), listenersObject.Array.Elements...) {
		listener, _ := listenerValue.Reference()
		if listener == 0 {
			continue
		}
		_, _, invokeErr := vm.InvokeVirtual(
			ctx,
			listener,
			"playerUpdate",
			"(Ljavax/microedition/media/Player;Ljava/lang/String;Ljava/lang/Object;)V",
			ReferenceValue(player),
			ReferenceValue(vm.NewString(event)),
			data,
		)
		if invokeErr != nil && !errors.Is(invokeErr, ErrMethodNotFound) {
			return invokeErr
		}
	}
	return nil
}

func (vm *VM) playerControl(receiver uint32, args []Value) (Value, bool, error) {
	if _, _, _, err := vm.requireOpenPlayer(receiver); err != nil {
		return Value{}, false, err
	}
	name, err := vm.stringArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	name = strings.TrimPrefix(name, "javax.microedition.media.control.")
	if name == "VolumeControl" {
		return ReferenceValue(vm.ensurePlayerControl(receiver, false)), true, nil
	}
	if name == "ToneControl" {
		return ReferenceValue(vm.ensurePlayerControl(receiver, true)), true, nil
	}
	return ReferenceValue(0), true, nil
}

func (vm *VM) ensurePlayerControl(playerReference uint32, tone bool) uint32 {
	object, _, _ := vm.player(playerReference)
	field, class := playerVolumeField, "javax/microedition/media/control/VolumeControl"
	if tone {
		field, class = playerToneField, "javax/microedition/media/control/ToneControl"
	}
	if value, ok := object.Fields[field]; ok {
		reference, _ := value.Reference()
		if reference != 0 {
			return reference
		}
	}
	reference := vm.NewObject(class, nil)
	control, _ := vm.Object(reference)
	control.Fields[controlPlayerField] = ReferenceValue(playerReference)
	object.Fields[field] = ReferenceValue(reference)
	return reference
}

func (vm *VM) controlPlayer(receiver uint32) (*audioClipState, int32, error) {
	value, err := objectField(vm, receiver, controlPlayerField)
	if err != nil {
		return nil, 0, err
	}
	playerReference, err := value.Reference()
	if err != nil {
		return nil, 0, err
	}
	_, clip, state, err := vm.requireOpenPlayer(playerReference)
	return clip, state, err
}

func (vm *VM) installMediaControlNatives() {
	vm.RegisterNative("javax/microedition/media/control/VolumeControl", "getLevel", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		clip, _, err := vm.controlPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
		return IntValue(int32(info.Volume)), true, err
	})
	vm.RegisterNative("javax/microedition/media/control/VolumeControl", "isMuted", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		clip, _, err := vm.controlPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
		return IntValue(boolInt(info.Muted)), true, err
	})
	vm.RegisterNative("javax/microedition/media/control/VolumeControl", "setLevel", "(I)I", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		clip, _, err := vm.controlPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		level, _ := intArgument(args, 0)
		level = max(0, min(100, level))
		info, _ := vm.services.Media.Info(vm.serviceOwner, clip.clip)
		if err := vm.services.Media.SetClipGain(vm.serviceOwner, clip.clip, uint8(level), info.Muted, info.Pan); err != nil {
			return Value{}, false, err
		}
		if int32(info.Volume) != level {
			if err := vm.notifyVolumeChanged(ctx, receiver); err != nil {
				return Value{}, false, err
			}
		}
		return IntValue(level), true, nil
	})
	vm.RegisterNative("javax/microedition/media/control/VolumeControl", "setMute", "(Z)V", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		clip, _, err := vm.controlPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		mute, _ := intArgument(args, 0)
		info, _ := vm.services.Media.Info(vm.serviceOwner, clip.clip)
		if err := vm.services.Media.SetClipGain(vm.serviceOwner, clip.clip, info.Volume, mute != 0, info.Pan); err != nil {
			return Value{}, false, err
		}
		if info.Muted != (mute != 0) {
			return Value{}, false, vm.notifyVolumeChanged(ctx, receiver)
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/media/control/ToneControl", "setSequence", "([B)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		clip, state, err := vm.controlPlayer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if state >= playerPrefetched {
			return Value{}, false, vm.newThrowable("java/lang/IllegalStateException", "")
		}
		sequenceReference, _ := referenceArgument(args, 0)
		if sequenceReference == 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		sequence, err := vm.ByteArray(sequenceReference)
		if err != nil {
			return Value{}, false, err
		}
		data, err := renderToneSequence(sequence)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", err.Error())
		}
		return Value{}, false, vm.services.Media.ReplaceSource(vm.serviceOwner, clip.clip, data)
	})
}

func (vm *VM) notifyVolumeChanged(ctx context.Context, control uint32) error {
	playerValue, err := objectField(vm, control, controlPlayerField)
	if err != nil {
		return err
	}
	player, err := playerValue.Reference()
	if err != nil {
		return err
	}
	return vm.notifyPlayerListeners(ctx, player, "volumeChanged", ReferenceValue(control))
}

func (vm *VM) installMediaStaticFields() {
	for name, value := range map[string]int32{"CLOSED": playerClosed, "UNREALIZED": playerUnrealized, "REALIZED": playerRealized, "PREFETCHED": playerPrefetched, "STARTED": playerStarted} {
		vm.RegisterStaticField("javax/microedition/media/Player", name, "I", IntValue(value))
	}
	vm.RegisterStaticField("javax/microedition/media/Player", "TIME_UNKNOWN", "J", LongValue(-1))
	vm.RegisterStaticField("javax/microedition/media/Manager", "TONE_DEVICE_LOCATOR", "Ljava/lang/String;", ReferenceValue(vm.NewString("device://tone")))
	for name, value := range map[string]int32{"VERSION": -2, "TEMPO": -3, "RESOLUTION": -4, "BLOCK_START": -5, "BLOCK_END": -6, "PLAY_BLOCK": -7, "SET_VOLUME": -8, "REPEAT": -9, "C4": 60, "SILENCE": -1} {
		vm.RegisterStaticField("javax/microedition/media/control/ToneControl", name, "B", IntValue(value))
	}
	for name, value := range map[string]string{"CLOSED": "closed", "DEVICE_AVAILABLE": "deviceAvailable", "DEVICE_UNAVAILABLE": "deviceUnavailable", "DURATION_UPDATED": "durationUpdated", "END_OF_MEDIA": "endOfMedia", "ERROR": "error", "STARTED": "started", "STOPPED": "stopped", "VOLUME_CHANGED": "volumeChanged"} {
		vm.RegisterStaticField("javax/microedition/media/PlayerListener", name, "Ljava/lang/String;", ReferenceValue(vm.NewString(value)))
	}
}

func (vm *VM) playManagerTone(note, duration, volume int32) error {
	const hiddenField = "$media.managerTone"
	key := fieldStorageKey("javax/microedition/media/Manager", hiddenField, "Ljavax/microedition/media/Player;")
	value := vm.hostStatic[key]
	playerReference, _ := value.Reference()
	var clip *audioClipState
	if playerReference != 0 {
		_, clip, _ = vm.player(playerReference)
	}
	if clip == nil || clip.clip == 0 {
		created, _, err := vm.newMIDPPlayer("audio/wav", nil)
		if err != nil {
			return err
		}
		playerReference, _ = created.Reference()
		_, clip, _ = vm.player(playerReference)
		vm.RegisterStaticField("javax/microedition/media/Manager", hiddenField, "Ljavax/microedition/media/Player;", ReferenceValue(playerReference))
	}
	if info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip); err == nil && info.State != shared.ClipStopped {
		_ = vm.services.Media.Stop(vm.serviceOwner, clip.clip)
	}
	if err := vm.services.Media.ReplaceSource(vm.serviceOwner, clip.clip, synthesizeToneWAV(note, duration, volume)); err != nil {
		return err
	}
	return vm.services.Media.Play(vm.serviceOwner, clip.clip, 1)
}

func synthesizeToneWAV(note, durationMS, volume int32) []byte {
	const sampleRate = 8000
	samples := max(1, int(durationMS)*sampleRate/1000)
	data := make([]byte, 44+samples*2)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], sampleRate)
	binary.LittleEndian.PutUint32(data[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(samples*2))
	frequency := 440.0 * math.Pow(2, float64(note-69)/12)
	amplitude := float64(volume) / 100 * 12000
	for index := range samples {
		sample := int16(math.Sin(2*math.Pi*frequency*float64(index)/sampleRate) * amplitude)
		binary.LittleEndian.PutUint16(data[44+index*2:], uint16(sample))
	}
	return data
}

func renderToneSequence(sequence []byte) ([]byte, error) {
	if len(sequence) < 4 || len(sequence)%2 != 0 || int8(sequence[0]) != -2 || sequence[1] != 1 {
		return nil, fmt.Errorf("tone sequence must start with VERSION 1")
	}
	tempo, resolution := int32(120), int32(64)
	index := 2
	if index+1 < len(sequence) && int8(sequence[index]) == -3 {
		modifier := int32(sequence[index+1])
		if modifier < 5 || modifier > 127 {
			return nil, fmt.Errorf("invalid TEMPO modifier %d", modifier)
		}
		tempo = modifier * 4
		index += 2
	}
	if index+1 < len(sequence) && int8(sequence[index]) == -4 {
		resolution = int32(sequence[index+1])
		if resolution < 1 || resolution > 127 {
			return nil, fmt.Errorf("invalid RESOLUTION %d", resolution)
		}
		index += 2
	}
	blocks := make(map[byte][]byte)
	for index+1 < len(sequence) && int8(sequence[index]) == -5 {
		block := sequence[index+1]
		start := index + 2
		index = start
		for index+1 < len(sequence) && int8(sequence[index]) != -6 {
			if int8(sequence[index]) == -5 {
				return nil, fmt.Errorf("nested tone blocks are invalid")
			}
			if int8(sequence[index]) == -9 {
				index += 4
			} else {
				index += 2
			}
		}
		if index+1 >= len(sequence) || sequence[index+1] != block {
			return nil, fmt.Errorf("unterminated tone block %d", block)
		}
		if _, exists := blocks[block]; exists {
			return nil, fmt.Errorf("duplicate tone block %d", block)
		}
		blocks[block] = append([]byte(nil), sequence[start:index]...)
		index += 2
	}
	volume := int32(100)
	events := make([]tonePCMEvent, 0, (len(sequence)-index)/2)
	if err := appendToneEvents(sequence[index:], blocks, tempo, resolution, &volume, &events, 0); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("tone sequence has no playable events")
	}
	return synthesizeToneEventsWAV(events)
}

type tonePCMEvent struct {
	note       int32
	durationMS int32
	volume     int32
}

func appendToneEvents(data []byte, blocks map[byte][]byte, tempo, resolution int32, volume *int32, events *[]tonePCMEvent, depth int) error {
	if depth > 16 {
		return fmt.Errorf("tone block nesting is too deep")
	}
	for index := 0; index < len(data); {
		if index+1 >= len(data) {
			return fmt.Errorf("truncated tone event")
		}
		command, value := int32(int8(data[index])), int32(data[index+1])
		switch command {
		case -7:
			block, ok := blocks[data[index+1]]
			if !ok {
				return fmt.Errorf("undefined tone block %d", value)
			}
			if err := appendToneEvents(block, blocks, tempo, resolution, volume, events, depth+1); err != nil {
				return err
			}
			index += 2
		case -8:
			if value < 0 || value > 100 {
				return fmt.Errorf("invalid tone volume %d", value)
			}
			*volume = value
			index += 2
		case -9:
			if value < 2 || index+3 >= len(data) {
				return fmt.Errorf("invalid tone repeat")
			}
			note, duration := int32(int8(data[index+2])), int32(data[index+3])
			if err := appendToneNote(note, duration, value, tempo, resolution, *volume, events); err != nil {
				return err
			}
			index += 4
		default:
			if err := appendToneNote(command, value, 1, tempo, resolution, *volume, events); err != nil {
				return err
			}
			index += 2
		}
	}
	return nil
}

func appendToneNote(note, duration, repeat, tempo, resolution, volume int32, events *[]tonePCMEvent) error {
	if note < -1 || note > 127 || duration < 1 || duration > 127 {
		return fmt.Errorf("invalid tone note %d duration %d", note, duration)
	}
	durationMS := max(1, duration*60_000/(tempo*resolution))
	for range repeat {
		*events = append(*events, tonePCMEvent{note: note, durationMS: durationMS, volume: volume})
	}
	return nil
}

func synthesizeToneEventsWAV(events []tonePCMEvent) ([]byte, error) {
	const sampleRate = 8000
	total := 0
	for _, event := range events {
		total += max(1, int(event.durationMS)*sampleRate/1000)
		if total > sampleRate*60*10 {
			return nil, fmt.Errorf("tone sequence exceeds ten minutes")
		}
	}
	data := make([]byte, 44+total*2)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], sampleRate)
	binary.LittleEndian.PutUint32(data[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(total*2))
	offset := 44
	for _, event := range events {
		samples := max(1, int(event.durationMS)*sampleRate/1000)
		frequency := 440.0 * math.Pow(2, float64(event.note-69)/12)
		amplitude := float64(event.volume) / 100 * 12000
		for index := range samples {
			var sample int16
			if event.note != -1 {
				sample = int16(math.Sin(2*math.Pi*frequency*float64(index)/sampleRate) * amplitude)
			}
			binary.LittleEndian.PutUint16(data[offset:], uint16(sample))
			offset += 2
		}
	}
	return data, nil
}
