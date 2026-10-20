# GNEX32 구현·검증 완료 체크리스트

작성일: 2026-10-20
완료일: 2026-10-20
브랜치: `feat/gnex32-completeness`

범위는 `gnex32/`, `loader/gnex`의 version-4 경로,
`application/gnex32_machine.go`, 그리고 GVM/GNEX 공용 제품 계약이다.
모든 항목은 구현 코드, 자동 테스트, SDK/코퍼스/기존 GVM 독립 근거를 함께
확인했다. 전체 현황과 수치는 [현재 GNEX 호환성 문서](../docs/gnex-format.md)에
정리했다.

## 0. 기준선과 사운드 버그

- [x] `PlaySound(0x82)` / `StopSound(0x83)`과 MMMD/MIDI/WAV PCM 출력을 구현했다. ([code](../gnex32/runtime.go), [test](../gnex32/audio_test.go), [reference](../application/gnex32_audio_reference_test.go))
- [x] GNEX32 PCM을 guest-time 기반 `Machine.DrainAudio`로 전달한다. ([code](../application/gnex32_machine.go), [test](../application/gnex32_audio_test.go), [contract](../core/machine.go))
- [x] pause/stop/reset/close/save/load의 buffered PCM 및 generation 수명주기를 고정했다. ([code](../application/gnex32_machine.go), [test](../application/gnex32_audio_test.go), [state test](../application/gnex32_state_test.go))
- [x] splash, title, 여섯 메인 메뉴 항목, help, opening/gameplay 입력을 실제 Virus 코퍼스로 실행한다. ([product test](../application/gnex32_reference_test.go), [reference](../docs/gnex-format.md#verification))

## 1. 기존 no-op 서비스

- [x] `SetGamma(0x25)`의 0..6 범위를 공용 SKT 팔레트와 RGB 이미지/그리기 색상에 적용했다. ([code](../runtime/skt_palette.go), [runtime](../gnex32/display.go), [test](../gnex32/runtime_graphics_test.go))
- [x] `SetFont(0x29)`를 여섯 SDK 크기로 구현하고 `SetFontType(0x28)`과 분리했다. ([code](../gnex32/runtime.go), [test](../gnex32/runtime_graphics_test.go), [reference](../docs/gnex-format.md#graphics-and-text))
- [x] `SetKeyTone(0x90)`을 DTMF/1 kHz, 60 ms, 일반 sound와의 동시 mixing 계약으로 구현했다. ([code](../gnex32/keytone.go), [test](../gnex32/audio_test.go), [product test](../application/gnex32_audio_test.go))
- [x] `SetBackLight(0x91)`의 off/on/MMI 상태, profile 기본값, save/load를 구현했다. ([code](../gnex32/runtime.go), [test](../gnex32/backlight_test.go), [state](../gnex32/state_test.go))

## 2. 머신 수명주기와 상태 보존

- [x] `gnex32Machine.SaveState` / `LoadState`를 구현했다. ([code](../application/gnex32_state.go), [test](../application/gnex32_state_test.go), [reference](../docs/gnex-format.md#state-and-persistence))
- [x] PC, halt/fault, value/call/mark stack과 lvalue reference를 스냅샷한다. ([code](../gnex32/state.go), [test](../gnex32/state_test.go), [VM](../gnex32/vm.go))
- [x] symbol RAM, mutable media, palette override를 스냅샷한다. ([code](../loader/gnex/gnex32_memory.go), [media](../loader/gnex/gnex32_media.go), [test](../gnex32/state_test.go))
- [x] tick, timers, presents, PRNG, paint/font, handset state, UserNV를 보존한다. ([code](../gnex32/state.go), [test](../gnex32/state_test.go), [replay](../gnex32/state_test.go))
- [x] working/latest/saved LCD와 clip rectangle을 보존한다. ([code](../gnex32/state.go), [test](../gnex32/state_test.go), [display](../gnex32/display.go))
- [x] 재생 clip, decoder 위치, shared clock, queued PCM을 보존한다. ([code](../gnex32/state.go), [test](../gnex32/state_test.go), [product test](../application/gnex32_state_test.go))
- [x] 상태 포맷 version/checksum/bounds/cross-process 검증을 추가했다. ([code](../application/gnex32_state.go), [test](../application/gnex32_state_test.go), [codec](../gnex32/state.go))
- [x] `GetUserNV/PutUserNV`를 reset/relaunch 영속 저장에 연결했다. ([code](../application/gnex32_savedata.go), [test](../application/gnex32_savedata_test.go), [runtime test](../gnex32/runtime_nv_test.go))
- [x] ZIP의 `.dat`, `user.dat`, `UserNV.sys` 초기값을 구조적으로 선택한다. ([code](../application/gnex32_savedata.go), [test](../application/gnex32_savedata_test.go), [loader](../loader/gnex/package.go))
- [x] reset은 UserNV만 보존하고 휘발성 VM/미디어/화면/시간을 재생성한다. ([code](../application/gnex32_machine.go), [test](../application/gnex32_savedata_test.go), [reference](../docs/gnex-format.md#state-and-persistence))
- [x] close 후 framebuffer/present/audio 접근과 모든 자원 정리를 nil-safe하게 했다. ([code](../application/gnex32_machine.go), [test](../application/gnex32_presentation_test.go), [audio test](../application/gnex32_audio_test.go))

## 3. 시간, 타이머, 이벤트, 입력

- [x] `GetDate/GetTime`을 device timezone을 가진 deterministic shared clock으로 교체했다. ([code](../gnex32/runtime.go), [test](../gnex32/runtime_device_test.go), [replay](../gnex32/state_test.go))
- [x] 세 timer의 60 Hz ceil, once/repeat, catch-up, slot 0→1→2 순서를 구현했다. ([code](../gnex32/runtime.go), [test](../gnex32/timer_test.go), [reference](../docs/gnex-format.md#time-timers-and-events))
- [x] 동일 tick callback 실패를 fail-stop/transactional 순서로 고정했다. ([code](../gnex32/runtime.go), [test](../gnex32/timer_test.go), [state](../gnex32/state.go))
- [x] 11개 header callback slot을 이름 있는 이벤트로 노출하고 필요한 host producer를 연결했다. ([loader](../loader/gnex/gnex32.go), [runtime](../gnex32/runtime.go), [test](../gnex32/event_test.go))
- [x] key 1..22, release `0xff`, 500 ms/50 ms repeat, soft/send/end를 전수 구현했다. ([code](../gnex32/input.go), [test](../gnex32/input_test.go), [product mapping](../application/gnex32_machine.go))
- [x] pause/resume 중 timer, repeat, audio guest clock이 함께 멈추고 재개된다. ([code](../application/gnex32_machine.go), [test](../application/gnex32_device_test.go), [timer test](../gnex32/timer_test.go))

## 4. 그래픽과 텍스트

- [x] `FlushPartial`의 inclusive clipped 부분 갱신을 구현했다. ([code](../gnex32/display.go), [test](../gnex32/display_test.go), [runtime test](../gnex32/runtime_graphics_test.go))
- [x] `CopyImageDir`의 normal/mirror 값과 그 외 오류를 고정했다. ([code](../gnex32/display.go), [test](../gnex32/display_test.go), [reference](../docs/gnex-format.md#graphics-and-text))
- [x] `CopyImageEx`의 alpha 0..5, zoom, mirror, rotation 모든 조합을 구현했다. ([code](../gnex32/display.go), [test](../gnex32/display_test.go), [VM test](../gnex32/vm_test.go))
- [x] alpha 단계와 혼합식을 픽셀 단위로 검증했다. ([code](../gnex32/display.go), [test](../gnex32/display_test.go), [reference](../docs/gnex-format.md#graphics-and-text))
- [x] mono `0x02/0x05` Color0/Color1 의미를 고정했다. ([decoder](../loader/gnex/gnex32_image.go), [test](../loader/gnex/gnex32_image_test.go), [render test](../gnex32/display_test.go))
- [x] indexed `0x06/0x07/0x09/0x0a/0x0b` palette와 RGB 채널 순서를 고정했다. ([decoder](../loader/gnex/gnex32_image.go), [corpus test](../loader/gnex/gnex32_image_reference_test.go), [render test](../gnex32/display_test.go))
- [x] `Mode=1`의 palette index 0 투명, `Mode=0` 불투명 규칙을 고정했다. ([code](../gnex32/display.go), [test](../gnex32/display_test.go), [reference](../docs/gnex-format.md#graphics-and-text))
- [x] `SetPaletteColorRGB`를 일곱 image kind와 save/load에 구현했다. ([code](../loader/gnex/gnex32_media.go), [test](../loader/gnex/gnex32_media_test.go), [integration](../gnex32/runtime_graphics_test.go))
- [x] 실제 corpus의 image kind를 전수 스캔하고 사용되는 일곱 형식만 decoder에 포함했다. ([scan](../loader/gnex/gnex32_image_reference_test.go), [decoder](../loader/gnex/gnex32_image.go), [reference](../docs/gnex-format.md#graphics-and-text))
- [x] round rectangle, clip, inclusive/reversed 좌표를 검증했다. ([code](../gnex32/display.go), [test](../gnex32/display_test.go), [service test](../gnex32/runtime_graphics_test.go))
- [x] EUC-KR 오류, glyph baseline/advance, solid background와 alignment를 검증했다. ([code](../gnex32/runtime.go), [test](../gnex32/runtime_graphics_test.go), [decoder](../loader/gnex/header.go))
- [x] font 0..5 외 입력은 상태를 바꾸지 않고 오류를 반환한다. ([code](../gnex32/runtime.go), [test](../gnex32/runtime_graphics_test.go), [contract](../gnex32/services.go))
- [x] 화면 크기와 `swWidth/swHeight`를 device profile로 이동했다. ([code](../gnex32/runtime.go), [test](../gnex32/profile_test.go), [application](../application/gnex32_machine.go))
- [x] video sequence/timestamp/generation을 audio와 같은 guest timeline으로 노출한다. ([contract](../core/machine.go), [code](../application/gnex32_machine.go), [test](../application/gnex32_presentation_test.go))

## 5. 오디오

- [x] Virus의 flag-`0x200` MMMD 30개를 전부 decode한다. ([decoder](../gnex32/runtime.go), [corpus test](../application/gnex32_audio_reference_test.go), [reference](../docs/gnex-format.md#audio-and-handset-controls))
- [x] 메뉴/BGM/SFX에서 비무음 PCM과 guest 시작 시점을 검증했다. ([product test](../application/gnex32_reference_test.go), [audio test](../application/gnex32_audio_reference_test.go), [contract](../core/machine.go))
- [x] 1회 재생, 재호출 교체, 완주 event, `StopSound` 규칙을 구현했다. ([code](../gnex32/runtime.go), [test](../gnex32/audio_test.go), [event test](../gnex32/event_test.go))
- [x] 잘못된/미지원 사운드는 shared media 오류로 원자적으로 실패한다. ([code](../gnex32/runtime.go), [test](../gnex32/audio_test.go), [shared error](../runtime/media.go))
- [x] KeyTone과 일반 sound mixing/priority를 구현했다. ([code](../gnex32/keytone.go), [test](../gnex32/audio_test.go), [product test](../application/gnex32_audio_test.go))
- [x] 모든 머신 수명주기의 buffered PCM 회귀 테스트를 추가했다. ([test](../application/gnex32_audio_test.go), [state test](../application/gnex32_state_test.go), [runtime state](../gnex32/state_test.go))

## 6. VM opcode

- [x] main/header callback 전체의 reachable opcode를 정적 CFG로 수집하고 gameplay 동적 trace를 추가했다. ([analysis](../gnex32/analysis.go), [trace](../gnex32/trace.go), [product test](../application/gnex32_reference_test.go))
- [x] Virus reachable unknown opcode를 0건으로 만들었다. ([preflight](../gnex32/preflight.go), [test](../gnex32/preflight_test.go), [inspector](../cmd/aram-gnex32-inspect/main.go))
- [x] `0x61`, `0x63`, `0x67`, `0x6b`, `0x6c`, `0x71`, `0x8a`의 X/I/N/Z 대입·lvalue 조합을 구현했다. ([code](../gnex32/vm.go), [test](../gnex32/vm_test.go), [reference](../docs/gnex-format.md#vm-and-service-contracts))
- [x] 기존 candidate `0x03/04/05/0b/0f/10/74/75/85/86/8d/90/92`를 SDK assembler와 테스트로 확정했다. ([code](../gnex32/vm.go), [test](../gnex32/vm_test.go), [analysis](../gnex32/analysis.go))
- [x] 모든 구현 opcode의 truncation, underflow/overflow, invalid address, transactional failure를 표 기반으로 검증했다. ([test](../gnex32/vm_test.go), [errors](../gnex32/vm.go), [reference](../docs/gnex-format.md#vm-and-service-contracts))
- [x] 32-bit overflow, shift, division, signed comparison 경계를 검증했다. ([code](../gnex32/vm.go), [test](../gnex32/vm_test.go), [GVM reference](../gvm/vm.go))
- [x] call/return, stack mark, event 진입의 stack/ref 초기화 규칙을 검증했다. ([code](../gnex32/vm.go), [test](../gnex32/vm_test.go), [event test](../gnex32/event_test.go))

## 7. Host service

- [x] 모든 `0x92` call site와 registry 차이를 JSON으로 출력한다. ([analysis](../gnex32/analysis.go), [tool](../cmd/aram-gnex32-inspect/main.go), [test](../cmd/aram-gnex32-inspect/main_test.go))
- [x] 동적 gameplay trace가 service ID, offset, stack tail, lvalue 여부를 기록한다. ([trace](../gnex32/trace.go), [test](../gnex32/vm_test.go), [product test](../application/gnex32_reference_test.go))
- [x] Virus 정적 CFG의 `UnsupportedServiceError` 대상 20건을 모두 구현해 0건으로 만들었다. ([runtime](../gnex32/runtime.go), [preflight](../gnex32/preflight.go), [product test](../application/gnex32_reference_test.go))
- [x] `DrawRect`, 문자열/byte-array/MakeStr, math, browser/vibration, system info, network, sqrt 서비스를 구현했다. ([runtime](../gnex32/runtime.go), [service tests](../gnex32/runtime_string_test.go), [network tests](../gnex32/runtime_network_test.go))
- [x] 80개 service의 정확한 이름과 성공 pop/push 계약을 등록하고 runtime 결과와 강제 대조한다. ([contracts](../gnex32/services.go), [enforcement](../gnex32/runtime.go), [test](../gnex32/analysis_test.go))
- [x] identity, date/time, string/media bounds, alias/overlap, CP949, `ArrayToVar` 0..3, NV 16-word 원자성을 검증했다. ([device test](../gnex32/runtime_device_test.go), [string test](../gnex32/runtime_string_test.go), [NV test](../gnex32/runtime_nv_test.go))
- [x] `RandSeed/Rand`를 legacy GVM의 214013/2531011 sequence, 상한 배제, reversed/signed 범위로 맞췄다. ([code](../gnex32/runtime.go), [test](../gnex32/runtime_math_test.go), [independent reference](../gvm/vm.go))

## 8. Loader와 리소스

- [x] 11개 callback slot을 이름 있는 필드와 inspector 출력으로 노출했다. ([loader](../loader/gnex/gnex32.go), [test](../loader/gnex/gnex32_test.go), [tool](../cmd/aram-gnex32-inspect/main.go))
- [x] 과거 title checksum 필드가 application/provider ID임을 corpus로 확인했다. ([code](../loader/gnex/header.go), [test](../loader/gnex/reference_test.go), [reference](../docs/gnex-format.md#archive-and-metadata-decoding))
- [x] symbol `0x1/0x100`이 모두 writable이며 descriptor 초기값은 per-run copy임을 검증했다. ([code](../loader/gnex/gnex32_memory.go), [test](../loader/gnex/gnex32_memory_test.go), [corpus](../loader/gnex/reference_test.go))
- [x] media `0x1/0x101` writable과 `0/0x100/0x200` read-only class를 이름 있는 계약으로 고정했다. ([code](../loader/gnex/gnex32.go), [memory](../loader/gnex/gnex32_media.go), [test](../loader/gnex/gnex32_media_test.go))
- [x] `.res` `0xDEADFACE` launcher icon을 bounded decode한다. ([code](../loader/gnex/metadata.go), [test](../loader/gnex/metadata_test.go), [corpus](../loader/gnex/reference_test.go))
- [x] `.wmr` `0xFACEDEAD` bundle과 BMP/ABM records를 bounded decode한다. ([code](../loader/gnex/metadata.go), [test](../loader/gnex/metadata_test.go), [corpus](../loader/gnex/reference_test.go))
- [x] `.mod/.inf`의 실행 식별에 필요한 stable fields를 구조화했다. ([code](../loader/gnex/metadata.go), [test](../loader/gnex/metadata_test.go), [corpus](../loader/gnex/reference_test.go))
- [x] corpus에 version-4 변형이 하나뿐임을 명시하고 근거 없는 layout 일반화를 하지 않는다. ([decoder](../loader/gnex/gnex32.go), [corpus test](../loader/gnex/reference_test.go), [scope](../docs/gnex-format.md#compatibility-snapshot))
- [x] malformed/oversized image/symbol/media/archive fuzz seed와 bounds를 확장했다. ([fuzz](../loader/gnex/gnex32_test.go), [archive tests](../loader/gnex/package_test.go), [limits](../loader/gnex/package.go))

## 9. 제품 라우팅과 공용 계약

- [x] 단일 hash branch를 검증된 compatibility-profile registry로 일반화했다. ([code](../application/gnex32_machine.go), [test](../application/gnex32_device_test.go), [reference](../docs/gnex-format.md#compatibility-snapshot))
- [x] 구조적으로 유효하지만 unqualified인 GNEX32에 정확한 capability 진단을 제공한다. ([code](../application/machine_load.go), [test](../application/gnex_test.go), [preflight](../gnex32/preflight.go))
- [x] Virus identity/display/profile 상수를 device profile로 이동했다. ([code](../gnex32/runtime.go), [application](../application/gnex32_machine.go), [test](../gnex32/profile_test.go))
- [x] 다른 hash 실행 전 opcode/service/media preflight를 수행한다. ([code](../gnex32/preflight.go), [test](../gnex32/preflight_test.go), [analysis](../gnex32/analysis.go))
- [x] GVM/GNEX32가 공용 palette, normalized input, persistence, audio frontend 계약을 사용한다. ([palette](../runtime/skt_palette.go), [machine contract](../core/machine.go), [persistence](../application/gvm_savedata.go))
- [x] GVM v1/v2의 fixed persistent byte record도 `core.SaveDataMachine`으로 relaunch 가능하게 했다. ([code](../application/gvm_savedata.go), [test](../application/gvm_savedata_test.go), [store](../application/gvm_storage.go))

## 10. 최종 검증

- [x] `ARAM_TEST_DATA`를 재귀 탐색해 exact Virus SHA-256을 찾는 optional test를 사용한다. ([helper](../application/gnex32_reference_test.go), [loader corpus](../loader/gnex/reference_test.go), [audio corpus](../application/gnex32_audio_reference_test.go))
- [x] 장시간 자동 입력 probe를 추가했다: 3,745,463 instructions, 69 opcodes, 47/73 static services, 122 frame hashes, 6,116,608 PCM samples. ([test](../application/gnex32_reference_test.go), [trace](../gnex32/trace.go), [reference](../docs/gnex-format.md#verification))
- [x] 여섯 메인 메뉴 항목과 6-step wraparound를 독립 trace하고 최소 다섯 결과 분기를 확인했다. ([test](../application/gnex32_reference_test.go), [reference](../docs/gnex-format.md#verification), [preflight](../gnex32/preflight.go))
- [x] 정적 reachable unknown opcode와 unsupported service가 모두 0건이며, 동적 trace의 모든 service가 registry 계약을 통과한다. ([analysis](../gnex32/analysis.go), [contract](../gnex32/services.go), [product test](../application/gnex32_reference_test.go))
- [x] 동일 입력 replay와 save→진행→load→재진행의 frame/audio/state hash가 일치한다. ([test](../gnex32/state_test.go), [state](../application/gnex32_state_test.go), [runtime](../gnex32/state.go))
- [x] reset/reopen 뒤 UserNV는 유지되고 휘발 상태는 초기화된다. ([test](../application/gnex32_savedata_test.go), [code](../application/gnex32_savedata.go), [machine](../application/gnex32_machine.go))
- [x] Windows에서 환경변수 없는 `go test ./...`와 `go vet ./...`가 통과했다. GNEX 전용 corpus test도 별도 통과했다. ([workflow note](../docs/gnex-format.md#verification), [loader tests](../loader/gnex/reference_test.go), [product tests](../application/gnex32_reference_test.go))
- [x] `docs/gnex-format.md`의 역사적/상충하는 미구현 문구를 현재 구현 문서로 교체했다. ([document](../docs/gnex-format.md), [VM](../gnex32/vm.go), [loader](../loader/gnex/gnex32.go))
- [x] 모든 완료 항목에 코드·테스트·참조 근거 링크를 연결했다. ([this checklist](gnex32-unimplemented-checklist.md), [compatibility document](../docs/gnex-format.md), [branch history](../.git/logs/HEAD))

## 검증 메모

- 상용 게임의 엔딩까지 수동 완주했다고 주장하지 않는다. 대신 여섯 메인 메뉴
  항목 전수, opening/gameplay 장시간 입력, static CFG 전수, 그리고 미도달 library
  service의 직접 계약 테스트를 결합했다.
- `ARAM_TEST_DATA`를 GNEX 전용 하위 폴더로 둔 채 전체 저장소 참조 테스트를 실행하면
  BREW/KTF/J2ME 코퍼스가 없어서 실패하는 것이 정상이다. 전체 suite는 환경변수 없이,
  GNEX 참조 suite는 해당 폴더를 설정해 분리 실행한다.
- `.tmp`의 다른 파일과 `.codex-investigate/`는 사용자 소유 자료라 변경하지 않았다.
