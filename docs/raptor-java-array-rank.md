# Raptor Java array rank

Module 100 ordinal 14 receives the array rank in `r0`, the reference component
in `r1`, and the JVM primitive type code in `r2`. A rank-one primitive token
retains the existing descriptor-character representation. Higher primitive
ranks retain both the rank and the leaf descriptor in an opaque scalar token.

Ordinal 16 allocates one dimension. Therefore `byte[][]` needs four-byte
reference slots even though its leaf type is a byte. Its host mirror must also
be a reference array, and must not enter primitive-array synchronization.
Ordinal 17 consumes a rank at each allocated level, preserving primitive leaf
widths and partially allocated arrays.

## Regression evidence

Issues #307 and #324 report the same Legend of Master input:

`735a579d82ac53bb205b04250ce44586c6d9375e64c2d6f3324796b3ae24d031`

The old bridge discarded ordinal 14's rank, allocating a 33-element `byte[][]`
as a 33-byte body. Reference stores then overwrote neighboring array headers
and host mirrors. A later `String(byte[], offset, count)` read pointers as its
length and count and failed. This was not a string slice calling convention
difference in this reproduction.

A local debugger scenario starts the game, advances 1,000 quanta, presses OK
four times (16 held quanta and 500 following quanta each), presses hash for 16
quanta, advances 1,000 quanta, and repeats OK twelve times with the same timing.
At a 750,000-instruction run budget, the old implementation fails at quantum
6,668. The fix completes all 10,272 quanta, passing the cave scene into dialogue
in the next area. This verifies the reported transition for the exact input;
it does not establish whole-game compatibility.

Synthetic tests exercise primitive ranks one through four, byte/short/int
leaves, all 33 reference stores, intact child headers and mirrors, subsequent
String construction, and full/partial multidimensional allocation. The
rank-two byte-array case fails against the old ordinal-14 implementation:
its mirror has one-byte primitive elements instead of four-byte references.

Private input bytes, memory dumps, screenshots, and debugger output are not
part of the repository.
