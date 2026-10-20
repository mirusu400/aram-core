# GNEX32 미구현/미검증 체크리스트

작성일: 2026-10-20

범위: `gnex32/`, `loader/gnex`의 version-4 경로, `application/gnex32_machine.go`.
현재 저장소에서 확인 가능한 명시적 no-op, typed unsupported 경로, 임시 호환
동작, 원본 검증이 끝나지 않은 동작을 모두 포함한다. 과거 조사 과정을 서술하는
`docs/gnex-format.md` 중 "미구현" 문구 일부는 현재 코드보다 오래되었으므로,
아래 목록은 현재 코드를 기준으로 한다.

체크 기준: 구현 코드, 단위 테스트, 실제 또는 독립적인 참조 근거가 모두 있어야
항목을 완료(`[x]`)로 바꾼다. 근거 없이 성공 no-op으로 바꾸는 것은 완료로 보지 않는다.

## 0. 이미 완료되어 이번 목록에서 제외하는 기준선

- [x] `PlaySound(0x82)` / `StopSound(0x83)` 및 MMMD/MIDI/WAV PCM 출력
- [x] GNEX32 오디오를 `Machine.DrainAudio`로 전달하는 guest-time 타임라인
- [x] `Reset` 시 오디오 generation 변경 마커 발행
- [x] 현재 Virus 경로의 화면, 키 입력, 세 타이머 슬롯, 문자열/NV 기본 동작

## 1. 명시적으로 no-op인 서비스

- [x] `0x25 Gamma`의 정확한 인자/범위를 확인하고 실제 팔레트·그리기 색상에 적용한다.
  - SDK의 `SetGamma(int)` 계약대로 0..6, 기본값 3을 적용한다.
  - SKT gamma 표를 `runtime` 공용 위치로 옮겨 GVM/GNEX32가 함께 사용한다.
- [x] `0x29 Font`의 정확한 계약을 복원하고 구현한다.
  - SDK의 `SetFont(size)` 계약대로 크기 0..5만 변경한다.
  - `0x28 SetFontType(size, foreground, background, alignment)`의 색·배경·정렬 계약과 분리한다.
- [x] `0x90 KeyTone`을 공유 미디어/합성 톤 경로로 구현한다.
  - SDK가 on/off만 정의하므로 숫자·`*`·`#`는 표준 DTMF, 기타 키는 1 kHz, 길이는 60 ms인 호환성 정책을 고정했다.
  - 반복 키는 이전 KeyTone만 교체하고 `SetKeyTone(0)`은 현재 톤을 중단하며, 일반 sound와는 공유 타임라인에서 혼합한다.
- [x] `0x91 BackLight` 상태를 구현한다.
  - SDK의 `S_OFF=0`, `S_ON=1`, `S_MMI=2`를 보존하고 공유 Device 및 머신 진단 API에 노출한다.
  - 헤드리스 프로필의 `S_MMI`는 조명 켜짐으로 해석하며 reset 기본값도 `S_MMI`로 고정한다. SaveState 복원은 상태 저장 작업에서 함께 검증한다.

## 2. 머신 수명주기와 상태 보존

- [x] `gnex32Machine.SaveState`를 구현한다.
- [x] `gnex32Machine.LoadState`를 구현한다.
- [x] VM 상태를 스냅샷한다: PC, halt/fault 상태, value/call/mark stack, lvalue refs.
- [x] symbol RAM과 mutable media RAM을 스냅샷한다.
- [x] 런타임 상태를 스냅샷한다: tick, timers, presents, PRNG, paint/font, UserNV.
- [x] 디스플레이 상태를 스냅샷한다: working/latest/saved LCD와 clip rectangle.
- [x] 재생 중인 sound clip, shared clock, queued PCM 및 오디오 generation을 스냅샷한다.
- [x] 저장 상태 포맷에 version, bounds, corruption validation 및 cross-process 테스트를 추가한다.
- [x] `PutUserNV/GetUserNV`를 reset 및 앱 재실행 뒤에도 유지되는 영속 저장소에 연결한다.
- [x] ZIP의 `.dat`, `user.dat`, `UserNV.sys`를 초기/기존 UserNV와 연결한다.
- [x] `Reset`이 UserNV는 보존하고 휘발성 VM/미디어/화면 상태만 초기화하도록 분리한다.
- [x] `Stop`에서 이미 프런트엔드에 전달된 오디오를 무효화하는 generation 마커를 발행한다.
- [x] `Close`에서 오디오 generation을 무효화하고 media/coordinator 자원을 정리한다.
- [x] close 후 `Framebuffer`, `PresentCount`, `DrainAudio`가 nil panic 없이 일관된 계약을 따른다.

## 3. 시간, 타이머, 이벤트

- [x] `GetDate(4)` / `GetTime(5)`의 `time.Now()` 사용을 shared deterministic clock과 장치 timezone으로 교체한다.
- [x] 날짜/시간이 save/load, replay, 동일 입력 재실행에서 결정적인지 테스트한다.
  - [x] query가 shared clock을 변경하지 않고 동일 clock state에서 결정적인지 확인했다.
- [x] `SetTimer0..2`의 60 Hz 반올림, 최초 발화 시점, repeat catch-up 및 동일 tick 순서를 원본과 대조한다.
  - SDK의 `S_TM_ONCE=0`, `S_TM_REPEAT=1`, `t=0` 정지 계약과 60 Hz 호스트의 ceil 반올림을 고정했다.
- [x] 여러 타이머가 같은 frame에 만료될 때의 callback 순서와 오류 원자성을 확정한다.
  - slot 0→1→2 순서이며 실패한 callback의 timer/VM fault까지만 커밋하고 이후 slot과 shared clock advance는 중단한다.
- [ ] header callback slot 1과 4..10의 의미를 식별하고 필요한 이벤트를 연결한다.
  - 현재 main은 slot 0, timer는 slot 2, key는 slot 3만 사용한다.
- [ ] press/release 코드, 키 반복, soft/send/end/숫자키 전체를 실제 GNEX32 계약과 대조한다.
- [x] pause/resume 중 타이머와 오디오 clock 정지/재개 동작을 명시적으로 테스트한다.

## 4. 그래픽과 텍스트

- [x] `FlushPartial(0x60)`이 현재 무시하는 x1/y1/x2/y2를 사용해 부분 갱신을 구현한다.
- [x] `CopyImageDir`의 direction 값 전체를 확인한다.
  - SDK Table 6은 `S_DIR_NORMAL=0`, `S_DIR_MIRROR=1`만 정의하며 그 외 값은 오류 처리한다.
- [x] `CopyImageEx`의 zoom, mirror, rotation 조합을 구현한다.
  - SDK의 alpha 0..5, zoom 0..1, mirror 0..1, rotation 0..3 전체 조합을 지원한다.
- [ ] alpha 혼합식과 alpha 단계 의미를 참조 구현/실기와 대조한다.
- [ ] mono kind `0x02/0x05`의 Color0/Color1 의미를 확정한다.
- [ ] indexed kind `0x06/0x07/0x09/0x0a/0x0b`의 palette 형식과 채널 순서를 확정한다.
- [ ] indexed image의 `Mode`와 palette index 0 투명 처리 규칙을 확정한다.
- [ ] `SetPaletteColorRGB`를 해당되는 모든 image kind에 대해 구현하고 불가능한 형식은 명확히 오류 처리한다.
- [ ] 알려진 다른 image kind가 실제로 그리기에 사용되는지 corpus를 스캔하고, 사용되는 형식만 decoder를 추가한다.
- [x] `Gamma` 적용 전후의 고정 팔레트 및 동적 팔레트 동작을 통합 테스트한다.
- [ ] `DrawRectRound/FillRectRound`, clip, inclusive 좌표 규칙을 참조 화면과 대조한다.
- [ ] EUC-KR 오류 처리, glyph baseline/advance, solid background 높이와 정렬을 원본 폰트와 대조한다.
- [ ] font size 0..5 외 값의 native fallback/error 동작을 확인한다.
- [ ] 하드코딩된 120x149 화면과 `swWidth/swHeight`를 장치 profile로 이동한다.
- [ ] frame timestamp/sequence를 오디오와 같은 guest timeline으로 노출하는 presentation 계약을 추가한다.

## 5. 오디오 후속 작업

- [ ] 실제 Virus의 flag-`0x200` MMMD 리소스 전체가 decode되는지 corpus 테스트를 추가한다.
- [ ] 실제 메뉴/BGM/SFX 동선에서 비무음 PCM, 시작 시점, 정지 시점, 교체 동작을 검증한다.
- [ ] native `PlaySound`의 1회 재생, 재호출 교체, 완주 후 상태 및 `StopSound` 규칙을 대조한다.
- [ ] 잘못된/지원되지 않는 사운드 리소스가 게임 전체를 fault시킬지 guest 오류값을 반환할지 계약을 확인한다.
- [x] `KeyTone`과 일반 sound가 동시에 울릴 때 mixing/priority 규칙을 구현한다.
- [ ] pause, stop, reset, close, save/load 각각의 buffered PCM 처리 회귀 테스트를 추가한다.

## 6. VM opcode 완성도

- [ ] 실제 reference image의 도달 가능한 명령을 정적 CFG와 동적 trace로 전부 수집한다.
- [ ] 현재 `default`의 `UnsupportedOpcodeError`에 도달하는 경로를 full-playthrough로 찾고 opcode별로 구현한다.
- [ ] 아래 candidate opcode 의미를 SDK assembler 또는 독립 참조 실행과 대조한다.
  - [ ] `0x03` indexed symbol-word push
  - [ ] `0x04` 32-bit symbol-word push
  - [ ] `0x05` signed 16-bit immediate push
  - [ ] `0x0b` scalar symbol pop
  - [ ] `0x0f` decrement
  - [ ] `0x10` duplicate
  - [ ] `0x74` direct symbol copy
  - [ ] `0x75` direct immediate assignment
  - [ ] `0x85` call
  - [ ] `0x86` return/halt boundary
  - [ ] `0x8d` indexed symbol lvalue
  - [ ] `0x90` store through lvalue
  - [ ] `0x92` service dispatch 및 stack-effect 규칙
- [ ] 모든 구현 opcode에 truncation, underflow/overflow, invalid address 및 transactional failure 테스트를 추가한다.
- [ ] native overflow/shift/division/부호 비교 규칙을 32-bit 경계값으로 대조한다.
- [ ] call/return, stack mark, event 진입 때 stack/ref 초기화 규칙을 검증한다.

## 7. host service 완성도

- [ ] 실제 image의 모든 `0x92 <service-id>` 호출을 정적 수집하고 현재 switch와 차이를 생성하는 도구를 만든다.
- [ ] 동적 full-playthrough trace로 실제 도달 service ID와 인자 stack을 기록한다.
- [ ] 현재 `default`의 `UnsupportedServiceError`에 도달하는 service를 ID별로 구현한다.
- [ ] service별 정확한 pop/push 수와 반환값/오류 sentinel을 SDK 문서 또는 참조 실행과 대조한다.
- [ ] `GetSysMin/GetSysUserID`의 하드코딩 identity를 device profile로 이동한다.
- [x] `GetDate/GetTime` 배열 배치와 고정 timezone/weekday 규칙을 검증한다.
- [x] `GetChar`의 음수 index 및 writable media one-past-end에서 0을 반환하는 현재 호환 동작을 검증한다.
- [x] `StrInit/StrLen/StrCpy/StrSub/StrCmp`의 NUL, CP949, alias/overlap 및 반환값 규칙을 검증한다.
- [x] `ArrayToVar`의 operation 0..3 의미와 추가 operation 존재 여부를 확인한다.
- [ ] `RandSeed/Rand`의 LCG, 범위 상한 배제, 음수 범위를 native 결과와 대조한다.
- [x] `GetUserNV/PutUserNV`의 실제 최대 word 수, 반환값 및 실패 원자성을 확인한다.
- [x] `Gamma/Font/KeyTone/BackLight`가 구현될 때 save/load 상태에도 포함한다.

## 8. loader, 리소스, 포맷 일반화

- [ ] GNEX32 header의 11개 code pointer slot 의미를 식별하고 이름 있는 필드로 노출한다.
- [ ] title checksum 알고리즘을 확인하고 선택적 검증을 추가한다.
- [ ] symbol flag `0x1/0x100`의 정확한 mutability/initialization 의미를 검증한다.
- [ ] media flag `0x1/0x101`을 writable로 취급하는 현재 provisional 규칙을 검증한다.
- [ ] media flag `0`, `0x100`, `0x200`의 type/ownership 의미를 확정한다.
- [ ] `.res`(`0xDEADFACE`) 외부 아이콘 형식을 decode할 필요가 있는지 확인하고 필요 시 구현한다.
- [ ] `.wmr` 번들 형식을 식별하고 런타임 리소스로 사용되는 경우 loader를 구현한다.
- [ ] `.mod`/`.inf`의 device/profile 정보 중 실행에 필요한 필드를 구조화한다.
- [ ] 현재 단일 prefix/layout 이외의 GNEX version-4 변형을 corpus 근거가 있을 때 추가한다.
- [ ] malformed/oversized image, symbol, media, archive에 대한 fuzz corpus를 확대한다.

## 9. 제품 라우팅과 호환성 범위

- [ ] `createGNEX32Machine`의 단일 SHA-256 허용을 compatibility/profile 판정으로 일반화한다.
- [ ] 구조적으로 유효하지만 미지원인 GNEX32 패키지에 정확한 capability 진단을 제공한다.
- [ ] hard-coded Virus identity/display/profile 값을 profile 데이터로 이동한다.
- [ ] `application/machine_load.go`의 오래된 "GNEX v4 미구현" fallback 문구를 현재 상태에 맞춘다.
- [ ] 다른 GNEX32 hash를 실행하기 전 opcode/service/media capability preflight를 추가한다.
- [ ] GVM v1/v2 경로와 GNEX32 경로의 공용 palette, input, persistence, audio 계약을 정리한다.

## 10. 검증과 완료 조건

- [ ] `ARAM_TEST_DATA`에서 Virus SHA-256 패키지를 CI/로컬 optional test가 찾도록 corpus 경로를 정리한다.
- [ ] splash → title → menu → help → gameplay 이후의 장시간 자동 입력 probe를 만든다.
- [ ] 최소 한 번의 완주 또는 모든 상태/메뉴를 포괄하는 trace에서 unsupported opcode/service가 0건임을 확인한다.
- [x] 동일 입력 두 번의 frame hash, audio hash, timer/NV 결과가 일치하는 determinism test를 추가한다.
- [x] save → 진행 → load → 재진행 결과가 frame/audio/state hash까지 동일한지 검증한다.
- [ ] reset/reopen 뒤 UserNV가 유지되고 휘발 상태는 초기화되는 제품 테스트를 추가한다.
- [ ] Windows 및 지원 대상 빌드에서 전체 `go test ./...`, `go vet ./...`를 통과한다.
- [ ] 구현 결과에 맞게 `docs/gnex-format.md`의 역사적/상충하는 미구현 문구를 정리한다.
- [ ] 체크리스트의 각 완료 항목에 코드·테스트·참조 근거 링크를 덧붙인다.

## 구현 권장 순서

1. 명시적 no-op 4개와 deterministic clock
2. Stop/Close 오디오 수명주기 및 UserNV persistence
3. SaveState/LoadState
4. graphics 제한(`FlushPartial`, transform, palette/gamma/font)
5. opcode/service 수집 도구와 full-playthrough trace
6. 실제로 도달한 unknown opcode/service 구현
7. loader/profile 일반화와 다른 hash 지원
8. 전체 참조 검증 및 문서 정리
