# Third-party notices

## Yamaha SMAF Player

The SMAF parser, event decoder, FM synthesis, custom-voice decoding, and Yamaha
ADPCM support in `runtime/smaf_*.go` are Go adaptations of concepts and code
from [Yamaha SMAF Player](https://github.com/akustikrausch/yamaha-smaf-player).
Those files were modified and integrated into ARAM.

Copyright 2026 Akustikrausch (Andreas Wendorf)

Licensed under the Apache License, Version 2.0. A copy is provided at
`third_party/yamaha-smaf-player/LICENSE`.

## fmFM

The Yamaha mobile-FM envelope timing, level curves, frequency scaling,
feedback depths, and operator waveform behavior in `runtime/smaf_fm.go` are
adapted from [fmFM](https://github.com/but80/fmfm).

Copyright (c) 2018 but80

Licensed under the MIT License. A copy is provided at
`third_party/fmfm/LICENSE`.

## ARAM handset Hangul bitmap

The compressed antialiased 12x12 Hangul and symbol glyph data in
`runtime/hangul_bitmap.go` was generated from NeoDunggeunmo. The derived
bitmap uses the neutral "ARAM handset bitmap" name rather than a reserved
font name.

Copyright (c) 2017-2021, Eunbin Jeong (Dalgona.)

The bitmap font data is licensed under the SIL Open Font License, Version 1.1.
A copy is provided at `third_party/neodgm/LICENSE`.

## ARAM handset crisp bitmap

The compressed 12x12 Hangul and symbol glyph data in
`runtime/galmuri9_bitmap.go` was generated from Galmuri9 (part of the Galmuri
bitmap font family) via `internal/cmd/gen-handset-font`. The derived bitmap
uses the neutral "ARAM handset crisp" name rather than a reserved font name.

Copyright (c) 2019-2025 Lee Minseo (quiple@quiple.dev)

The bitmap font data is licensed under the SIL Open Font License, Version 1.1.
A copy is provided at `third_party/galmuri/LICENSE`.
