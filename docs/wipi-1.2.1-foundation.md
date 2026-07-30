# WIPI 1.2.1 application foundation

ARAM's first native-application execution milestone uses the reconstructed
[WIPI Wiki index](https://mirusu400.github.io/wipi-wiki/llms.txt) as the
searchable standard reference and keeps device evidence separate from standard
requirements.

The standard-facing design facts used by the current implementation are:

- the platform executes downloaded
  [machine-code applications](https://mirusu400.github.io/wipi-wiki/overview/platform.md);
- C and Java applications use platform Basic APIs while device access is
  isolated behind the
  [HAL/platform boundary](https://mirusu400.github.io/wipi-wiki/overview/architecture.md);
- WIPI's HAL types define fixed 8-, 16-, 32-, and 64-bit values and a 32-bit
  address type in the
  [type definitions](https://mirusu400.github.io/wipi-wiki/hal/types.md);
- applications have independent execution and memory spaces, with lifecycle,
  events, shared memory, and platform-owned cleanup described by the
  [kernel API](https://mirusu400.github.io/wipi-wiki/c-api/kernel.md);
- graphics use an explicit framebuffer and only become visible after the
  platform flush boundary described by the
  [graphics API](https://mirusu400.github.io/wipi-wiki/c-api/graphics.md);
- display geometry, scanline layout, color depth, and RGB masks are described
  by the
  [HAL framebuffer contract](https://mirusu400.github.io/wipi-wiki/hal/frame-buffer.md);
- physical key values and key press/release/repeat events come from the
  [HAL system contract](https://mirusu400.github.io/wipi-wiki/hal/system.md),
  while device-specific game-action mapping uses the
  [virtual-key contract](https://mirusu400.github.io/wipi-wiki/hal/virtual-key.md);
- device and carrier extensions are returned as string-valued system
  properties, also defined by the
  [HAL system contract](https://mirusu400.github.io/wipi-wiki/hal/system.md).

These facts map to ARAM as follows:

| WIPI requirement | ARAM owner |
|---|---|
| 32-bit guest execution | `cpu.Backend` and `cpu/interpreter` |
| isolated text, BSS, stack | `application.Machine` |
| WIPI/carrier/OEM/device/title behavior | layered `profile` data |
| framebuffer and input contracts | headless `core.Machine` |
| host presentation and document selection | `aram-frontend` |

The `profile.Stack` merge order is standard, carrier, manufacturer, device,
then title. A title layer requires a SHA-256 key so one title's compatibility
behavior cannot silently become a carrier-wide or WIPI-wide rule. Screen
profiles preserve the HAL's width, height, bits-per-pixel, depth,
bytes-per-line, color type, and non-overlapping RGB masks. Key profiles use
the signed 32-bit `MH_KeyCode` values and the standardized virtual game-action
values.

ABHS and EADS container layouts, Samsung addresses, and the
`MinigameQVGAOEM` entry point are evidence-derived facts from
`anycall_magichole`; they are not presented as WIPI-standard formats. Product
tests use synthetic inputs by default and access the private reference only
through `ARAM_REFERENCE_REPO`.

## Current gate

The implemented gate is:

1. inspect and hash a bounded source;
2. validate its ABHS/EADS records;
3. map the selected EADS text and zeroed BSS into a 32-bit guest address space;
4. initialize a separate stack and Thumb entry context;
5. select the recovered title runtime only for the known DAT SHA-256
   `955a39b3c09d6228224234dab18b3b38fe89da518c0b614a7cba47e6f9f96900`;
6. build deterministic system, trampoline, application-heap, image-heap,
   resource-object, and 13-service table state;
7. execute bootstrap, setup, start, preload-frame, and visible-frame events
   through the portable interpreter and host service dispatcher;
8. expose ready, paused, stopped, and faulted states through the frontend
   integration adapter;
9. save and restore backend-identified, source-bound machine state using
   explicit little-endian fixed-width fields and a SHA-256 checksum.

For that hash, the private oracle proves the exact event instruction counts
`1771`, `160`, `1958`, `194`, and `36045`; service-call counts `46`, `16`, `1`,
`4`, and `308`; two presents; 32 ms of virtual time; and RGBA SHA-256
`0ae34e616ac40a0dab1e35d907acfef63fb47bd2b065875f17631f0bbeb915a7`.
It also proves that restoring the first-frame save state and advancing one
frame twice yields the same pixels and event accounting.

This is a title-specific compatibility result, not a claim that the opaque
service-slot meanings generalize to Samsung EADS applications, SKT WIPI, or
WIPI 1.2.1 as a whole.
