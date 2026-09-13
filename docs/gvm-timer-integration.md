# GVM timer integration evidence

Status: research checkpoint, not an implemented timer service or game-start
milestone. Opcode `9a` remains unsupported. This note distinguishes authenticated
static relationships, observed public-library behavior and missing integration.

## Scope and observed use

Reference observations are qualified by SHA-256
`c2c170b3c47cd99dc55fdadad31dd1927473b4362868205c5eb49eff96c60719`.
They do not establish another emulator version or a live handset's state. No
reference executable was run and no proprietary tables or payloads are included.

The [kernel diagnostic](../gvm/README.md#explicit-random-range-a1) reaches
`9a` at offset116 after4611 completed instructions with explicitly selected RNG
seeds. A public-stack predicate observation for diagnostic seed1 establishes
depth2, interval at least10, and a positive nonzero second operand. Therefore
this boundary needs an active timer service, not the short-interval bypass.
This is still descriptor-only kernel execution, not ordinary product startup.

## Authenticated reference relationships

The following are static contracts, conditional on valid native state and
normal returns. They are not a portable implementation or failure policy.

- `9a` uses signed16 interval from second-top and selector from top.
  It mirrors both raw words into native state, calls its helper and pops two.
  Intervals below10 bypass timer installation. They do **not** cancel an
  existing timer. Intervals10..32767 request milliseconds through `SetTimer`
  with window-associated ID `0x1000` and a null timer callback.
- The selected handler's ID `0x1000` branch tests the stored selector only for
  zero. Zero attempts `KillTimer`; any nonzero value bypasses that cancellation.
  It is not a decrementing count. Both branches clear the stored interval and
  call outer callback slot `0x538ea8` with arguments `(3,0)`.
  Cancellation is not an exactly-once guarantee, and queued events are separate.
- Three direct assignments of `0x41fe50` to that slot were authenticated in
  two containing bodies. In the loader-containing function, writer `0x409fb1`
  dominates a direct initial call to `0x41fe50(0,0)` in its normal local control
  flow. Other target assignments exist as decoded candidates. This proves an
  initialization path, not the slot's value throughout a selected execution.
- The main-dialog constructor installs a vtable whose map getter returns a
  record owning a `WM_TIMER` entry for `0x4097a0`. The ownership chain and entry
  are authenticated under the independently documented MFC4 layout. Native
  signature13 remains opaque: the exact framework argument unpacker, live
  window/class and actual message delivery were not verified.
- Conditional on outer callback `0x41fe50` receiving `(3,0)`, it constructs
  record selector0, subtype2 and a zero second argument. Its direct consumer
  calls **another** mutable slot, `0x5272d8`, with
  `(2,0,existing_dword[0x538f36])`. The third argument is not established as zero
  or a guest entry. Outer event3 and record subtype2 must not be relabeled as
  an authenticated guest event2 callback.

The inner target and existing record field are not file-backed initialized
values. Conditional writer anchors `0x412577` and `0x4126d7` occur in the
consumer, but their selected initialization path and lifetime remain unproved.
No guest header entry or dispatcher reset has yet been joined to this route.

## Existing runtime boundary

The shared [Timers](../runtime/timers.go) and [EventBus](../runtime/events.go)
already own virtual deadlines and serializable guest-neutral events. Callback
ABI translation belongs in the adapter, not the shared runtime or frontend.

Uncached existing ordering and queue-failure tests passed. A separate authored
caller of their actual public APIs also observed:

- advancing across three repeating periods queues three distinct expiries;
- cancellation prevents future expiry but preserves pending events;
- replacement preserves pending events while changing the next expiry/value;
- a one-shot becomes inactive when its expiry is enqueued;
- restored timer/queue snapshots reproduce pending-event order and values;
- insufficient queue capacity rolls back both timer and queue state.

These synthetic-input library observations are not Windows-message parity.
Do not automatically use catch-up-all-periods behavior or enqueue-time
deactivation for a reference handler that cancels during delivery. Decide and
test coalescing, pending-event, replacement and failure rules explicitly.

## Implementation prerequisites

1. Establish the selected inner-slot initialization/lifetime and either the
   record field's provenance or the selected target's proven non-use of it.
2. Join that target to the actual guest callback entry and dispatcher reset.
   Define initialization, ownership, reentrancy, budgets and sticky-fault policy.
3. Supply explicit serialized timer state, readiness, virtual-time advancement,
   cancellation, reset and teardown. A timer request without guest delivery
   must not be reported as an operational service.
4. Resolve default rendering independently. Normal-orientation component ramps
   are direct byte lookups in writable initialized data; no equivalent formula
   or runtime immutability has been established. Rotated colors or a blank
   allocated framebuffer must not substitute for an evidenced guest frame.
5. Exercise the ordinary product lifecycle and required workspace gates before
   claiming startup or merge readiness. The six configured KTF/Raptor missing
   package prerequisites remain a failed private gate, despite public/build/CI
   success. Research and library progress do not waive that requirement.

Public layout/context references:
[MFC TN006](https://learn.microsoft.com/en-us/cpp/mfc/tn006-message-maps?view=msvc-170),
[MFC4-compatible entry layout](https://vxl.github.io/doc/release/core/vgui/html/vgui__win32__cmdtarget_8h_source.html),
[WM_TIMER](https://learn.microsoft.com/en-us/windows/win32/winmsg/wm-timer).
The public compatible layout is not the exact reference framework source.
