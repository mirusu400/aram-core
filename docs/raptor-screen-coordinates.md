# Raptor screen coordinates

A Raptor Clet owns the whole screen. Three things describe it, and they agree:

- `MC_grpGetDisplayInfo` reports the physical size (240x320 on the usual LGT
  handset);
- the private height import (module 507, ordinal 52) answers the same height;
- the private pixel-pointer import (ordinal 50) names the first physical row.

Public WIPI drawing, pixel reads and presentation use those same coordinates.
Offscreen buffers have their own origin, as before.

## Why the handset strip was removed

Until issue #587 the primary surface started below a 24-row handset strip: the
height import answered 296 and the pixel pointer was advanced by 24 rows, while
`MC_grpGetDisplayInfo` still reported 320. Titles read both heights. Many take
the difference as an offset they have to apply themselves, so they drew every
screen 24 rows lower and lost the bottom 24 rows. Others built a 240x320 back
buffer and flushed it over a screen they had been told was 296 rows high. Seven
exact packages had been listed as full-surface exceptions one at a time.

Running all 104 Raptor titles of the LGT corpus on the whole surface showed no
title that needed the strip. Titles that size a back buffer from the height
import simply build a 240x320 one. The exception list is no longer consulted
while a title runs.

Issues #323 and #325 (public rectangle primitives 24 rows away from directly
rendered pixels in Zenonia and Terra) stay fixed, because both paths now start
at the first row.

## Guard rows

Clets clip their own raw-pixel blits, and a few write outside the screen: up to
24 rows above the first row (놈ZERO, issue #284) or below the last one
(MapleStory Thief). The screen's pixels therefore sit between two 24-row guard
areas inside one heap allocation. The framebuffer descriptor keeps the visible
size, and the guards are never presented. Without them such a write lands in
whichever heap object happens to be adjacent.

## Save states

A Clet caches the geometry it read at start, so a save state keeps the geometry
it was written with. Raptor state schema 6 records the screen origin. An older
state is read as starting below the 24-row strip, unless its exact package is in
`quirkdb.RaptorLegacyFullSurfaceTitles`, the former exception list, which is
kept for this purpose only. A state that still uses the strip keeps it when it
is saved again. Restarting the title returns it to the whole screen.

## Tests

Synthetic tests cover the private imports and the in-guest pixel helper for
both geometries, the guard rows around the screen, loading a state whose screen
pixels start inside their allocation, and the origin a legacy state is given.
`wipi_screen_origin_test.go` keeps the public drawing view correct for states
that still use the strip. No game bytes or screenshots are in the repository.
