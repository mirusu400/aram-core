# GVM timer integration evidence

Status: request-boundary checkpoint, not an implemented timer service or
game-start milestone. Opcode `9a` can now forward its authenticated signed16
interval and raw16 selector to an explicitly supplied atomic sink. The sink does
not by itself make the kernel install a shared timer, advance time, deliver a
callback or redispatch the guest. This note distinguishes that narrow kernel
boundary from authenticated
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
This remains descriptor-only kernel execution, not game startup.

The ordinary product now exposes the same limit only under the explicit
`gvm-kernel-v1/skt/diagnostic` profile. Its Machine runs a bounded initial
dispatch and reports opcode `9a` as a terminal `timer-request` service boundary
without accepting the request. The default profile remains recognition-only.
The diagnostic machine publishes no frame and exposes no continue/frame-step,
input, save-state or timer-delivery capability.

With the new explicit request sink, the same selected corpus identity forwards
that request and reaches guest `ff` after4616 completed instructions. Both the
raw descriptor state and the incomplete scalar-prefix preparation reach the same
buffer-relative halt at44943, differing by the previously measured six setup
instructions. The probe sink intentionally performs no installation or delivery,
so this proves only that the initial bounded dispatch can finish after forwarding
the request. It is not timer operation, event delivery, a frame or game startup.

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
The latter installs `0x4148f0` only for input selector6 with mode0 and directly
calls it with `(0,1,0)`. This is not the loader's initial selector2 route.

If the live target is `0x4148f0`, its event2 path forwards only the second
argument to `0x41e450`; the third argument is unused on that particular path.
The helper performs conditional mirror updates and word writes through native
state pointers, then calls `0x40ed50`. Only after normal return does it read
LE16 at `H+0x20` (32 bytes in decimal), with H freshly loaded from `0x5276b8`,
and call the dispatcher. This establishes a conditional entry-source/reset
join, not selected startup or permission to skip preceding state effects.
Nonzero native entry resets operand and return indices; zero entry does not
undo the preceding helper work. The saved-top scalar is distinct.

A bounded initializer recheck maps the pointer aliases used here to reserved
symbols0,3,4,5,6. These are aliases of guest storage, not independent counters.
The header pointer H is published from the buffer base before initialization
completes, so its presence alone does not prove readiness. Ordered writes and
fresh reads must preserve overlapping-symbol effects.

The complete `0x40ed50` body has no calls or external tail transfer. Under its
ordinary native-state prerequisites it publishes its argument, copies182 bytes
from a configuration-selected lookup source, and updates a mapped byte. This
is local lookup-state mutation, not presentation. The source contents, current
configuration and safe source/destination extents remain unresolved. No copied
table or guessed replacement is provided. Thus the helper-body gap is closed,
but successful initialization and portable lookup-state policy still block
operational timer delivery.

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
2. Establish the conditional wrapper's actual header/state-pointer readiness
   and helper effects before using its entry/reset join. Define initialization,
   ownership, reentrancy, budgets and sticky-fault policy. The bounded
   [BeginDispatch API](../gvm/README.md#explicit-dispatch-re-entry) supplies only
   post-halt kernel mechanics, not those wrapper prerequisites or delivery.
3. Connect the request sink to explicit serialized timer state, readiness,
   virtual-time advancement, cancellation, reset and teardown. The implemented
   request boundary without guest delivery must not be reported as an operational
   timer service.
4. Resolve default rendering independently. Normal-orientation component ramps
   are direct byte lookups in writable initialized data; no equivalent formula
   or runtime immutability has been established. Rotated colors or a blank
   allocated framebuffer must not substitute for an evidenced guest frame.
5. Extend the now-tested explicit product diagnostic beyond its service
   breakpoint only after the prerequisites above are established. The six
   configured KTF/Raptor missing package prerequisites remain a failed private
   gate, despite public/build/CI success. A diagnostic load is not startup or
   merge readiness.

Public layout/context references:
[MFC TN006](https://learn.microsoft.com/en-us/cpp/mfc/tn006-message-maps?view=msvc-170),
[MFC4-compatible entry layout](https://vxl.github.io/doc/release/core/vgui/html/vgui__win32__cmdtarget_8h_source.html),
[WM_TIMER](https://learn.microsoft.com/en-us/windows/win32/winmsg/wm-timer).
The public compatible layout is not the exact reference framework source.
