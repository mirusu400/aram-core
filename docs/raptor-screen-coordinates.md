# Raptor screen coordinates

The default Raptor screen has a 24-pixel reserved strip above its drawable area.
The private pixel-pointer and height imports already expose this client area.
Public WIPI drawing and pixel reads now use a view of that same memory: the
pixel address advances by 24 rows and the drawable height shrinks by 24 rows.
Offscreen buffers retain their own origin. Allocation descriptors, snapshots,
and presentation retain the physical screen. Profiles with an explicit primary
framebuffer height retain their existing origin.

This fixes issues #323 and #325. These titles combine guest pixel rendering
with public rectangle primitives. Previously those paths differed by 24 rows:
Zenonia's slot fill requested y=104 while its directly rendered label appeared
in the client area below the reserved strip. The repaired slots and confirmation
dialogue align; Terra's consent text and buttons align with their panel too.
The local inputs matched the SHA-256 identities in both reports.

Synthetic regression tests compare 12 drawing/pixel operations against an
offscreen buffer, preserve the top strip, and check client-area reads, copies,
BMP encoding, and full-screen presentation. The existing Raptor raw-pointer and
configured-height tests continue to pass. No game bytes or screenshots are
included in the repository.
