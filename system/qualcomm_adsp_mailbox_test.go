package system

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestQualcommADSPMailboxAcknowledgesHostWrites(t *testing.T) {
	mailbox, err := NewQualcommADSPMailbox(0x100, 0x08)
	check(t, err)
	check(t, mailbox.Write(0x08, Width32, 0x80020000))
	if got, err := mailbox.Read(0x08, Width32); err != nil || got != 0x00020000 {
		t.Fatalf("write-request acknowledgement = %#x, %v", got, err)
	}
	check(t, mailbox.Write(0x08, Width32, 0x90020000))
	if got, err := mailbox.Read(0x08, Width32); err != nil || got != 0x70000000 {
		t.Fatalf("write-done acknowledgement = %#x, %v", got, err)
	}
	check(t, mailbox.Write(0x04, Width32, 0x11223344))
	if got, err := mailbox.Read(0x04, Width32); err != nil || got != 0x11223344 {
		t.Fatalf("ordinary mailbox register = %#x, %v", got, err)
	}
}

func TestQualcommADSPMailboxProcessesProfiledHostCommand(t *testing.T) {
	shared, _ := NewLatchedRegisterWindow(0x20, Width16)
	payload, _ := NewLatchedRegisterWindow(0x20, Width32)
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	profile := &QualcommADSPHostCommandProfile{
		SelectorWindowID: "shared", SelectorOffset: 0x08, SelectorWidth: Width16,
		Rules: []QualcommADSPHostCommandRuleProfile{{
			Command: 1,
			Copies: []QualcommADSPMemoryCopyProfile{{
				SourceWindowID: "payload", SourceOffset: 0x0c,
				DestinationWindowID: "payload", DestinationOffset: 0x08, Width: Width32,
			}},
		}},
	}
	if err := mailbox.configureHostCommand(profile, map[string]*LatchedRegisterWindow{
		"shared": shared, "payload": payload,
	}); err != nil {
		t.Fatal(err)
	}
	_ = shared.Write(0x08, Width16, 1)
	_ = payload.Write(0x0c, Width32, 0x11223344)
	check(t, mailbox.Write(0x08, Width32, 0x80020000))
	if got, _ := shared.Read(0x08, Width16); got != 0 {
		t.Fatalf("host command selector = %#x", got)
	}
	if got, _ := payload.Read(0x08, Width32); got != 0x11223344 {
		t.Fatalf("host command response = %#x", got)
	}
	_ = shared.Write(0x08, Width16, 2)
	check(t, mailbox.Write(0x08, Width32, 0x80020000))
	if got, _ := shared.Read(0x08, Width16); got != 2 {
		t.Fatalf("unknown host command selector = %#x", got)
	}
}

func TestQualcommADSPMailboxAcknowledgesPayloadlessHostCommand(t *testing.T) {
	selector, err := NewLatchedRegisterWindow(0x10, Width16)
	check(t, err)
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	if err := mailbox.configureHostCommand(&QualcommADSPHostCommandProfile{
		SelectorWindowID: "selector", SelectorOffset: 0x08, SelectorWidth: Width16,
		Rules: []QualcommADSPHostCommandRuleProfile{{Command: 4}},
	}, map[string]*LatchedRegisterWindow{"selector": selector}); err != nil {
		t.Fatal(err)
	}
	check(t, selector.Write(0x08, Width16, 4))
	check(t, mailbox.Write(0x08, Width32, 0x80020000))
	value, err := selector.Read(0x08, Width16)
	check(t, err)
	if value != 0 {
		t.Fatalf("payloadless host-command selector = %#x, want 0", value)
	}
}

func TestQualcommADSPMailboxAppliesProfiledControlRules(t *testing.T) {
	shared, err := NewLatchedRegisterWindow(0x10, Width16)
	check(t, err)
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	if err := mailbox.configureControlRules([]QualcommADSPControlRuleProfile{
		{Offset: 0, Value: 2, Writes: []QualcommADSPMemoryWriteProfile{{
			WindowID: "shared", Offset: 0x0c, Width: Width16, Value: 1,
		}}},
		{Offset: 0, Value: 3, Writes: []QualcommADSPMemoryWriteProfile{{
			WindowID: "shared", Offset: 0x0c, Width: Width16, Value: 0,
		}}},
	}, map[string]*LatchedRegisterWindow{"shared": shared}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		control uint32
		want    uint32
	}{{control: 2, want: 1}, {control: 3, want: 0}} {
		check(t, mailbox.Write(0, Width32, step.control))
		value, readErr := shared.Read(0x0c, Width16)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if value != step.want {
			t.Fatalf("control %d shared response = %#x, want %#x", step.control, value, step.want)
		}
	}
}

func TestQualcommADSPMailboxPublishesResponseBeforeInterrupt(t *testing.T) {
	shared, err := NewLatchedRegisterWindow(0x20, Width16)
	check(t, err)
	vic, err := NewQualcommVectoredInterruptController(QualcommVectoredInterruptConfig{
		SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
	}, nil)
	check(t, err)
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	check(t, mailbox.configureControlRulesWithInterrupts(
		[]QualcommADSPControlRuleProfile{{
			Offset: 4, Value: 1,
			Writes: []QualcommADSPMemoryWriteProfile{{
				WindowID: "shared", Offset: 0x0c, Width: Width16, Value: 1,
			}},
			Interrupt: &QualcommADSPInterruptProfile{
				Source: 33, UseVectoredController: true,
			},
		}},
		map[string]*LatchedRegisterWindow{"shared": shared},
		nil,
		vic,
	))
	check(t, mailbox.Write(4, Width32, 1))
	if response, err := shared.Read(0x0c, Width16); err != nil || response != 1 {
		t.Fatalf("shared response = %#x, %v", response, err)
	}
	if pending := vic.PendingStatusBanks(); pending != [2]uint32{0x00008000, 0} {
		t.Fatalf("pending interrupt banks = %#v", pending)
	}
}

func TestQualcommADSPMailboxDefersAndSerializesProfiledResponses(t *testing.T) {
	shared, err := NewLatchedRegisterWindow(0x20, Width16)
	check(t, err)
	vic, err := NewQualcommVectoredInterruptController(QualcommVectoredInterruptConfig{
		SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
	}, nil)
	check(t, err)
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	check(t, mailbox.configureControlRulesWithInterrupts(
		[]QualcommADSPControlRuleProfile{{
			Offset: 4, Value: 1, ResponseDelayInstructions: 4,
			Writes: []QualcommADSPMemoryWriteProfile{{
				WindowID: "shared", Offset: 0x0c, Width: Width16, Value: 1,
			}},
			Interrupt: &QualcommADSPInterruptProfile{
				Source: 33, UseVectoredController: true,
			},
		}},
		map[string]*LatchedRegisterWindow{"shared": shared},
		nil,
		vic,
	))
	for range 2 {
		check(t, mailbox.Write(4, Width32, 1))
	}
	if response, _ := shared.Read(0x0c, Width16); response != 0 {
		t.Fatalf("response was published synchronously: %#x", response)
	}
	if pending := vic.PendingStatusBanks(); pending != [2]uint32{} {
		t.Fatalf("interrupt was pulsed synchronously: %#v", pending)
	}
	check(t, mailbox.Advance(3))
	if response, _ := shared.Read(0x0c, Width16); response != 0 {
		t.Fatalf("early response = %#x", response)
	}
	check(t, mailbox.Advance(1))
	if response, _ := shared.Read(0x0c, Width16); response != 1 {
		t.Fatalf("first delayed response = %#x", response)
	}
	if len(mailbox.pendingResponses) != 1 {
		t.Fatalf("pending responses after first completion = %d", len(mailbox.pendingResponses))
	}
	check(t, vic.Write(qualcommVICAcknowledge0Offset, Width32, 0x00008000))
	check(t, shared.Write(0x0c, Width16, 0))
	check(t, mailbox.Advance(4))
	if response, _ := shared.Read(0x0c, Width16); response != 1 {
		t.Fatalf("second delayed response = %#x", response)
	}
}

func TestQualcommADSPMailboxRejectsUnwiredInterruptResponse(t *testing.T) {
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	err = mailbox.configureControlRules([]QualcommADSPControlRuleProfile{{
		Offset: 4, Value: 1,
		Interrupt: &QualcommADSPInterruptProfile{
			Source: 3, UseVectoredController: true,
		},
	}}, nil)
	if err == nil {
		t.Fatal("unwired ADSP interrupt rule was accepted")
	}
}

func TestQualcommADSPMailboxRejectsInvalidAccesses(t *testing.T) {
	for _, spec := range [][2]uint32{{0, 0}, {3, 0}, {4, 2}, {4, 4}} {
		if _, err := NewQualcommADSPMailbox(spec[0], spec[1]); err == nil {
			t.Fatalf("accepted mailbox size=%#x control=%#x", spec[0], spec[1])
		}
	}
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	if _, err := mailbox.Read(0x08, Width16); !errors.Is(err, ErrQualcommADSPMailboxMMIO) {
		t.Fatalf("wrong-width read error = %v", err)
	}
	if err := mailbox.Write(0x10, Width32, 0); !errors.Is(err, ErrQualcommADSPMailboxMMIO) {
		t.Fatalf("out-of-range write error = %v", err)
	}
}

func TestQualcommADSPMailboxStateRoundTrip(t *testing.T) {
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	_ = mailbox.Write(0x04, Width32, 0x11223344)
	state, err := mailbox.SaveState()
	check(t, err)
	_ = mailbox.Reset()
	check(t, mailbox.LoadState(state))
	if got, _ := mailbox.Read(0x04, Width32); got != 0x11223344 {
		t.Fatalf("restored register = %#x", got)
	}
	corrupt := append([]byte(nil), state...)
	corrupt[8]++
	if err := mailbox.LoadState(corrupt); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched control-offset state error = %v", err)
	}
	if got, _ := mailbox.Read(0x04, Width32); got != 0x11223344 {
		t.Fatalf("failed state load changed register to %#x", got)
	}
}

func TestQualcommADSPMailboxStateRoundTripPreservesDelayedResponse(t *testing.T) {
	shared, _ := NewLatchedRegisterWindow(0x20, Width16)
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	rules := []QualcommADSPControlRuleProfile{{
		Offset: 4, Value: 1, ResponseDelayInstructions: 4,
		Writes: []QualcommADSPMemoryWriteProfile{{
			WindowID: "shared", Offset: 0x0c, Width: Width16, Value: 7,
		}},
	}}
	check(t, mailbox.configureControlRules(rules, map[string]*LatchedRegisterWindow{"shared": shared}))
	check(t, mailbox.Write(4, Width32, 1))
	check(t, mailbox.Advance(2))
	state, err := mailbox.SaveState()
	check(t, err)
	check(t, mailbox.Reset())
	check(t, mailbox.LoadState(state))
	check(t, mailbox.Advance(1))
	if response, _ := shared.Read(0x0c, Width16); response != 0 {
		t.Fatalf("restored response completed early: %#x", response)
	}
	check(t, mailbox.Advance(1))
	if response, _ := shared.Read(0x0c, Width16); response != 7 {
		t.Fatalf("restored delayed response = %#x", response)
	}
}

func TestQualcommADSPMailboxMigratesVersionOneSubset(t *testing.T) {
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	legacy := make([]byte, 0x20)
	copy(legacy, "QAMB")
	binary.LittleEndian.PutUint32(legacy[4:8], qualcommADSPMailboxLegacyState)
	binary.LittleEndian.PutUint32(legacy[8:12], 0x08)
	binary.LittleEndian.PutUint32(legacy[12:16], 0x10)
	binary.LittleEndian.PutUint32(legacy[20:24], 0x11223344)
	if err := mailbox.LoadState(legacy); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("strict load accepted version-one state: %v", err)
	}
	check(t, mailbox.LoadStateSubset(legacy))
	if got, _ := mailbox.Read(4, Width32); got != 0x11223344 {
		t.Fatalf("migrated version-one register = %#x", got)
	}
}

func TestQualcommADSPMailboxMigratesLatchedWindowSubset(t *testing.T) {
	legacy, err := NewLatchedRegisterWindow(0x10, Width32)
	check(t, err)
	_ = legacy.Write(0x04, Width32, 0x11223344)
	_ = legacy.Write(0x08, Width32, 0x80020000)
	state, err := legacy.SaveState()
	check(t, err)
	mailbox, err := NewQualcommADSPMailbox(0x10, 0x08)
	check(t, err)
	if err := mailbox.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("strict load accepted legacy state: %v", err)
	}
	check(t, mailbox.LoadStateSubset(state))
	if got, _ := mailbox.Read(0x04, Width32); got != 0x11223344 {
		t.Fatalf("migrated ordinary register = %#x", got)
	}
	if got, _ := mailbox.Read(0x08, Width32); got != 0x00020000 {
		t.Fatalf("migrated pending request = %#x", got)
	}
	if binary.LittleEndian.Uint32(state[24:28]) != 0x80020000 {
		t.Fatal("migration mutated caller state")
	}
}
