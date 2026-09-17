package runtime

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMediaPrepareAndStoppedDestroyPreserveOutput(t *testing.T) {
	m, e := NewMedia(NewRegistry(32), DefaultMediaLimits())
	check(t, e)
	wave := pcmWave(8000, 1, []int16{1000, -1000, 2000, -2000})
	newClip := func(data []byte) ServiceID {
		t.Helper()
		id, e := m.CreateClip(1, "", 0)
		check(t, e)
		_, e = m.Append(1, id, data)
		check(t, e)
		return id
	}
	a := newClip(wave)
	check(t, m.Play(1, a, -1))
	check(t, m.Advance(0, time.Millisecond, NewEventBus(16, 32)))
	before := m.Snapshot()
	revision := m.OutputRevision()
	b := newClip(wave)
	check(t, m.Prepare(1, b))
	info, e := m.Info(1, b)
	check(t, e)
	if info.State != ClipStopped || info.Position != 0 || !info.Decoded {
		t.Fatalf("prepare changed playback: %+v", info)
	}
	check(t, m.DestroyClip(1, b, nil))
	c := newClip([]byte{1})
	if !errors.Is(m.Prepare(1, c), ErrMediaUnsupported) {
		t.Fatal("invalid source accepted")
	}
	check(t, m.DestroyClip(1, c, nil))
	if !errors.Is(m.Prepare(1, a), ErrInvalidState) {
		t.Fatal("prepared active clip")
	}
	if !errors.Is(m.DestroyClip(1, a, nil), ErrInvalidState) {
		t.Fatal("destroyed active clip")
	}
	after := m.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("prepare/destroy changed active clip or queued audio")
	}
	if revision != m.OutputRevision() {
		t.Fatal("prepare/destroy changed output generation")
	}
	check(t, m.Stop(1, a))
	if revision == m.OutputRevision() {
		t.Fatal("active stop no longer invalidates output")
	}
	if len(m.Drain().PCM16) != 0 {
		t.Fatal("active stop retained stale output")
	}
}
