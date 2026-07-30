package runtime

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestReplayDrivesInputAndClockWithoutTraceDependency(t *testing.T) {
	recordConfig := DefaultConfig()
	recordConfig.ReplayMode = ReplayRecord
	recorded, err := NewServices(recordConfig)
	if err != nil {
		t.Fatal(err)
	}
	recorded.Trace.SetEnabled(true)
	if err := recorded.QueueInput(5, "up", true, 0); err != nil {
		t.Fatal(err)
	}
	if err := recorded.Advance(5, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	log := recorded.Replay.Snapshot()
	log.Mode = ReplayPlayback
	log.Cursor = 0

	playbackConfig := recordConfig
	playbackConfig.ReplayMode = ReplayPlayback
	replayed, err := NewServices(playbackConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayed.Replay.Restore(log); err != nil {
		t.Fatal(err)
	}
	if err := replayed.QueueInput(5, "up", true, 0); err != nil {
		t.Fatal(err)
	}
	if err := replayed.Advance(5, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed.Clock.Snapshot(), recorded.Clock.Snapshot()) ||
		!reflect.DeepEqual(replayed.Input.Snapshot(), recorded.Input.Snapshot()) ||
		!reflect.DeepEqual(replayed.Events.Snapshot(), recorded.Events.Snapshot()) {
		t.Fatal("replay did not reproduce clock, input, and event state")
	}
	if replayed.Replay.Snapshot().Cursor != uint32(len(log.Entries)) {
		t.Fatal("replay did not consume every entry")
	}
}

func TestReplayMismatchDoesNotConsumeEntry(t *testing.T) {
	config := DefaultConfig()
	config.ReplayMode = ReplayPlayback
	services, err := NewServices(config)
	if err != nil {
		t.Fatal(err)
	}
	state := services.Replay.Snapshot()
	state.Entries = []ReplayEntry{{
		Sequence: 1,
		AtNS:     0,
		Kind:     ReplayInput,
		Owner:    1,
		Name:     "up",
		Value:    1,
	}}
	state.NextSequence = 2
	if err := services.Replay.Restore(state); err != nil {
		t.Fatal(err)
	}
	if err := services.QueueInput(1, "down", true, 0); err == nil {
		t.Fatal("mismatched replay input succeeded")
	}
	if services.Replay.Snapshot().Cursor != 0 || services.Events.Len() != 0 {
		t.Fatal("mismatched replay mutated cursor or event queue")
	}
}

func TestServicesStatePreservesRuntimeReplayModeChange(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Replay.SetMode(ReplayRecord); err != nil {
		t.Fatal(err)
	}
	if err := services.QueueInput(1, "fire", true, 0); err != nil {
		t.Fatal(err)
	}
	encoded, err := services.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	clone, err := NewServices(services.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.UnmarshalBinary(encoded); err != nil {
		t.Fatal(err)
	}
	if clone.Replay.Mode() != ReplayRecord ||
		!reflect.DeepEqual(clone.Replay.Snapshot(), services.Replay.Snapshot()) {
		t.Fatal("service state did not preserve the live replay mode")
	}
}

func TestReplayDataLimitIsAggregateAndAtomic(t *testing.T) {
	replay, err := NewReplay(
		ReplayLimits{MaxEntries: 4, MaxData: 3},
		ReplayRecord,
		1,
		"profile",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.Record(ReplayEntry{
		Kind:      ReplayNetworkResponse,
		ServiceID: makeServiceID(1, 1),
		Name:      replaySocketRead,
		Value:     2,
		Data:      []byte{1, 2},
	}); err != nil {
		t.Fatal(err)
	}
	before := replay.Snapshot()
	if _, err := replay.Record(ReplayEntry{
		Kind: ReplayDeviceResponse,
		Name: "battery",
		Data: []byte{3, 4},
	}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("aggregate replay limit error = %v", err)
	}
	if !reflect.DeepEqual(replay.Snapshot(), before) {
		t.Fatal("failed replay record mutated the log")
	}

	invalid := before
	invalid.Entries = append(invalid.Entries, ReplayEntry{
		Sequence: 2,
		Kind:     ReplayDeviceResponse,
		Name:     "battery",
		Data:     []byte{3, 4},
	})
	invalid.NextSequence = 3
	if err := replay.Restore(invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore over-limit replay error = %v", err)
	}
	if !reflect.DeepEqual(replay.Snapshot(), before) {
		t.Fatal("failed replay restore mutated the log")
	}
}

func TestReplayRestoreRejectsRuntimeIdentityMismatchAtomically(t *testing.T) {
	config := DefaultConfig()
	config.ReplayMode = ReplayPlayback
	services, err := NewServices(config)
	if err != nil {
		t.Fatal(err)
	}
	before := services.Replay.Snapshot()

	testCases := map[string]func(*ReplayState){
		"random seed": func(state *ReplayState) {
			state.RandomSeed++
		},
		"profile ID": func(state *ReplayState) {
			state.ProfileID = "different/profile"
		},
		"profile hash": func(state *ReplayState) {
			state.ProfileHash[0] ^= 0xff
		},
	}
	for name, mutate := range testCases {
		t.Run(name, func(t *testing.T) {
			invalid := before
			mutate(&invalid)
			if err := services.Replay.Restore(invalid); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("Restore identity mismatch error = %v", err)
			}
			if after := services.Replay.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatal("identity mismatch mutated replay state")
			}
		})
	}
}

func TestServicesRestoreRejectsMalformedReplaySemanticsAtomically(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	before := services.Snapshot()

	testCases := map[string]ReplayEntry{
		"socket length": {
			Kind: ReplayNetworkResponse, ServiceID: makeServiceID(7, 1),
			Name: replaySocketRead, Value: 2, Data: []byte{1},
		},
		"HTTP encoding": {
			Kind: ReplayNetworkResponse, ServiceID: makeServiceID(8, 1),
			Name: replayHTTPComplete, Value: 200, Data: []byte{1},
		},
		"device range": {
			Kind: ReplayDeviceResponse, Name: replayDeviceStatus,
			Value: 101,
		},
	}
	for name, entry := range testCases {
		t.Run(name, func(t *testing.T) {
			invalid := services.Snapshot()
			entry.Sequence = 1
			invalid.Replay.Entries = []ReplayEntry{entry}
			invalid.Replay.NextSequence = 2
			if err := services.Restore(invalid); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("Restore malformed replay error = %v", err)
			}
			if after := services.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatal("malformed replay mutated services")
			}
		})
	}
}

func TestExternalNetworkResponsesRecordAndDrivePlayback(t *testing.T) {
	recordConfig := DefaultConfig()
	recordConfig.ReplayMode = ReplayRecord
	recorded, err := NewServices(recordConfig)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := recorded.Network.OpenSocket(2, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorded.Network.ConnectSocket(
		2,
		socket,
		"127.0.0.1",
		1234,
	); err != nil {
		t.Fatal(err)
	}
	if err := recorded.CompleteSocketResponse(
		2,
		socket,
		true,
		0,
	); err != nil {
		t.Fatal(err)
	}
	if err := recorded.InjectSocketResponse(
		2,
		socket,
		[]byte("recorded"),
		time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	log := recorded.Replay.Snapshot()
	log.Mode = ReplayPlayback
	log.Cursor = 0

	playbackConfig := recordConfig
	playbackConfig.ReplayMode = ReplayPlayback
	playback, err := NewServices(playbackConfig)
	if err != nil {
		t.Fatal(err)
	}
	replayedSocket, err := playback.Network.OpenSocket(2, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if replayedSocket != socket {
		t.Fatalf("replayed socket ID = %s, want %s", replayedSocket, socket)
	}
	if err := playback.Network.ConnectSocket(
		2,
		replayedSocket,
		"127.0.0.1",
		1234,
	); err != nil {
		t.Fatal(err)
	}
	if err := playback.Replay.Restore(log); err != nil {
		t.Fatal(err)
	}
	// Playback ignores provider values and applies the captured responses.
	if err := playback.CompleteSocketResponse(
		2,
		replayedSocket,
		false,
		0,
	); err != nil {
		t.Fatal(err)
	}
	if err := playback.InjectSocketResponse(
		2,
		replayedSocket,
		[]byte("ignored"),
		time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	data, err := playback.Network.SocketRead(2, replayedSocket, 64)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "recorded" ||
		playback.Replay.Snapshot().Cursor != uint32(len(log.Entries)) {
		t.Fatalf(
			"playback response = %q cursor %d",
			data,
			playback.Replay.Snapshot().Cursor,
		)
	}
}

func TestExternalResponseReplayLimitFailureIsAtomic(t *testing.T) {
	config := DefaultConfig()
	config.ReplayMode = ReplayRecord
	config.Limits.Replay.MaxData = 1
	services, err := NewServices(config)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := services.Network.OpenSocket(1, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Network.ConnectSocket(
		1,
		socket,
		"127.0.0.1",
		7,
	); err != nil {
		t.Fatal(err)
	}
	if err := services.CompleteSocketResponse(1, socket, true, 0); err != nil {
		t.Fatal(err)
	}
	networkBefore := services.Network.Snapshot()
	eventsBefore := services.Events.Snapshot()
	replayBefore := services.Replay.Snapshot()
	if err := services.InjectSocketResponse(
		1,
		socket,
		[]byte{1, 2},
		time.Millisecond,
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("over-limit external response error = %v", err)
	}
	if !reflect.DeepEqual(services.Network.Snapshot(), networkBefore) ||
		!reflect.DeepEqual(services.Events.Snapshot(), eventsBefore) ||
		!reflect.DeepEqual(services.Replay.Snapshot(), replayBefore) {
		t.Fatal("failed external response capture was not atomic")
	}
}

func TestSaveRestoreContinuesWithSameFrameAndEventSequence(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := services.Graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 4, Height: 4, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Graphics.SetScreen(1, surface); err != nil {
		t.Fatal(err)
	}
	if _, err := services.Graphics.Present(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	encoded, err := services.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	clone, err := NewServices(services.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.UnmarshalBinary(encoded); err != nil {
		t.Fatal(err)
	}

	continueRun := func(current *Services) (FrameSnapshot, EventBusState) {
		t.Helper()
		if err := current.QueueInput(1, "fire", true, current.Clock.Monotonic()); err != nil {
			t.Fatal(err)
		}
		if err := current.Graphics.Line(
			1,
			surface,
			0,
			0,
			3,
			3,
			RGB(10, 20, 30),
		); err != nil {
			t.Fatal(err)
		}
		frame, err := current.Graphics.Present(1, surface, Rectangle{})
		if err != nil {
			t.Fatal(err)
		}
		return frame, current.Events.Snapshot()
	}
	frameA, eventsA := continueRun(services)
	frameB, eventsB := continueRun(clone)
	if !reflect.DeepEqual(frameA, frameB) ||
		!reflect.DeepEqual(eventsA, eventsB) {
		t.Fatal("save/restore continuation changed frame or event sequence")
	}
}
