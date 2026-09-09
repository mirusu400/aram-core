# WIPI Wiki 대응 구현 및 잔여 차이 보고서

감사일: 2026-09-10

기준 브랜치: `main` (`a7ae238`)

구현 확인점: `feat/wipi-java-emulation` (`fbaa856`)

비교 기준: [`wipi-wiki/llms.txt`](https://mirusu400.github.io/wipi-wiki/llms.txt)와 링크된 WIPI 1.2.1, WIPI 2.0/2.2, CLDC 1.1, MIDP 2.0 문서

이 문서는 단순히 클래스 이름이 존재하는지를 세지 않는다. 다음 세 단계를
분리해 확인했다.

1. 문서의 클래스/인터페이스를 로드할 수 있는가
2. 문서의 정확한 메서드 이름과 JVM descriptor가 해석되는가
3. 메서드가 고정값 스텁이 아니라 상태 변경, 예외, 콜백, I/O 또는 렌더링처럼
   관찰 가능한 동작을 수행하는가

이번 브랜치는 Java 에뮬레이션을 우선하여 발견된 누락을 구현했다. WIPI-C는
변경하지 않았으며, 해당 차이는 아래에 별도로 남겼다.

## 결론

초기 조사에서 가장 큰 차이였던 Java 표면은 이 브랜치에서 대부분 해소됐다.

| 영역 | `main` 조사 결과 | 현재 결과 |
|---|---|---|
| KTF WIPI Java 1.2.1 | 문서상 149개 타입은 있었지만 다수 메서드가 미선언 또는 스텁 | 발견된 core, I/O, event/IME, LWC, media, call 차이를 실제 동작으로 교체 |
| KTF WIPI Java 2.0/2.2 | 주요 확장 패키지와 실제 `BaseClip` 계층 부재 | 20개 타입 추가, 기존 타입 확장, 상태/예외/콜백 및 save-state 구현 |
| SKVM CLDC 1.1 | 실질 클래스 누락 25개, 다수 core/util/I/O 메서드 부재 | 이전 누락 타입을 추가하고 문서의 구체 메서드 descriptor 누락 0개로 확인 |
| SKVM MIDP 2.0 | 실질 클래스 누락 52개 | LCDUI, game, media, RMS, network, PKI, MIDlet을 구현; 구체 메서드 누락 0개로 확인 |
| WIPI-C | v1.2.1 정확 이름 8개, v2.0 113개, v2.2 104개 누락 | 이번 Java 우선 작업에서는 변경 없음 |

현재 KTF 호스트 표면에는 WIPI 1.2.1, 2.x 확장과 프로젝트 호환 타입을 합쳐
171개 클래스와 1,599개 메서드 선언이 있다. SKVM에는 `java.*` 네이티브
진입점 543개와 `javax.microedition.*` 진입점 522개가 등록된다. 이 수치는
규모를 나타낼 뿐 준수율로 사용하지 않았다.

## 감사 입력과 주의점

| 입력 | 바이트 | SHA-256 |
|---|---:|---|
| `llms.txt` | 95,899 | `6a21b8bb5f7a2f3383c54257bbb5c27a4c233039dff29506d2f6d6dfcce5f53e` |
| WIPI 1.2.1 full text | 5,507,250 | `98fb299de5978ca92ad8b397da41f05c61b049efb920f5cefc5de21d0ef537f8` |
| WIPI 2.0 full text | 1,190,283 | `6858fa025b664e66d60adf4114d5653d01c90039f8b97bbe2c1b61b26caf88fd` |
| WIPI 2.2 full text | 1,607,686 | `d6820ecb0554a0fa4c7ec9eb077750c496e2a4f1fab758c392290701b9451235` |

WIPI 2.x 문서 일부는 한 Markdown 페이지 안에 다른 클래스가 `## Class`
제목 없이 이어지고, `Lava/lang/String`, `mageObserver`, `(V)I` 같은 변환 오류가
있다. 따라서 최초 자동 비교에서 나온 KTF 2.x 누락 146개는 대부분 메서드가
앞 클래스에 잘못 귀속된 오탐이었다. 클래스 계층과 원래 Java 선언을 기준으로
재분류하고, 2.0과 2.2 사이의 다음 차이는 양쪽 descriptor를 모두 수용했다.

- `SMSMessage`의 문서상 `Byte[]`와 2.2의 `byte[]`
- `Message` setter가 `get...`으로 인쇄된 2.0 오탈자
- 2.0 인스턴스 메서드와 2.2 정적 메서드가 다른 `ResourceGroup` 선언

SKVM MIDP 문서에서는 중복을 제거한 public/protected 메서드 선언 543개를
추출했다. 추상 콜백과 애플리케이션이 구현해야 하는 listener 메서드를 분리한
뒤 구체 descriptor 누락은 0개였다. CLDC도 같은 방식으로 구체 descriptor
누락이 없음을 확인했다. 다만 descriptor 존재는 픽셀 단위 UI나 실제 이동통신망
동작까지 동일하다는 뜻은 아니므로 잔여 제한을 마지막 절에 따로 적었다.

## KTF WIPI Java 1.2.1 구현 결과

### Core Java

- `String`의 `String(StringBuffer)`, `startsWith(String,int)`, float/double
  `valueOf`와 `StringBuffer`의 배열·정수·실수 append/insert 계열을 구현했다.
- `PrintStream`의 print/println/write/flush/error 상태가 실제 출력 스트림에
  반영된다.
- `System.getProperty`, `Class.isArray`, `Class.isInterface`, 클래스 로딩과
  할당 가능성 검사가 실제 런타임 모델을 사용한다.
- 숫자 wrapper의 radix 검사, `NumberFormatException`, NaN, 무한대, signed
  zero 및 narrowing 경계를 구현하고 회귀 테스트를 추가했다.
- `Vector`, `Stack`, `Hashtable`, `Enumeration`, `Random`, `Thread`, monitor,
  `Calendar`와 `TimeZone`의 상태 및 규격 예외를 구현했다.

### I/O, 이벤트와 입력기

- byte/character stream, reader/writer, data stream, modified UTF-8, mark/reset,
  skip, EOF와 close 상태를 구현했다.
- `EventQueue`는 post/get/dispatch/hook를 실제 큐와 Java callback에 연결했다.
- `InputMethodHandler`는 공용 IME automata와 연결되어 지원 모드, 현재 모드,
  조합 문자열, 심볼 및 입력 처리를 수행한다.
- `File`, `FileSystem`, `DataBase`, `Message`, `Socket`, `HttpSocket`의 문서
  descriptor와 상태·범위 검사·예외 처리를 보완했다.

### LWC와 그래픽

- component/container/form/card 계층에 실제 위치, 크기, inset, layout,
  focus, traversal, key grab, validation과 scroll 상태를 추가했다.
- label, button, checkbox, combo, list, text, progress, scrollbar, command bar,
  annunciator 등의 paint와 입력 callback을 구현했다.
- 이미지 observer, 애니메이션 프레임, clip/translate, primitive drawing과
  repaint/serviceRepaints를 공용 graphics surface에 연결했다.

### 미디어와 통화

- clip buffer, player 생명주기, 위치 이동, loop, volume, listener 및 완료
  callback을 공용 media service와 연결했다.
- `Call`은 idle/dialing/connected/held 상태, 응답·종료·DTMF·PPP 조건 및
  잘못된 전이에 대한 `IOException`을 모델링한다.
- recording 경로는 명시적인 실패 상태를 반환하며 성공한 것처럼 흡수하지 않는다.

## KTF WIPI Java 2.0/2.2 구현 결과

기존 1.2.1 클래스에 2.x 메서드를 합치고 다음 20개 호스트 타입을 추가했다.

| 기능 | 추가 타입과 동작 |
|---|---|
| 그래픽 | `AnimateImage`, `ConstraintChecker`; 프레임·반복·observer·layout 상태 |
| 미디어 | 실제 `BaseClip` 계층, `PlayerListener`, `UnavailableException`, `Camera`, `StillClip`, `VideoClip`; player 할당, watermark, mode/control, preview, snapshot, clip area, mute/default volume |
| 주소록 | `Address`, `AddressBook`; 그룹, 필드, 레코드, 검색, 잠금, 단축번호 |
| 위치 | `StationLocationInfo`, `GPSConfig`, `GPSLocationInfo`, `GPSProvider`, `GPSException`, `GPSListener`; 설정과 비동기 callback |
| I/O | `IODevice`; open/read/write/seek/size/close와 독립 버퍼 |
| SMS | `SMSMessage`, `SMS`; payload 복사, 길이·번호 검사와 전송 결과 |
| 단말 리소스 | `ResourceGroup`; 그룹/리소스 검색, metadata, read/write/delete |

이 상태들은 KTF save-state에 포함되며 복원 시 slice/map을 깊은 복사한다.
카메라 snapshot은 현재 에뮬레이터 프레임을 JPEG로 만들고, GPS와 SMS는 아래의
가상 단말 정책을 따른다.

## SKVM CLDC 1.1 구현 결과

초기 조사에서 빠졌던 wrapper, reference, character stream과 예외/오류 타입을
추가했다. 주요 동작은 다음과 같다.

- `Boolean`, `Byte`, `Character`, `Short`, `Integer`, `Long`, `Float`,
  `Double`의 생성, 변환, 비교, parse와 static constant
- `Object.clone`, `Class`, `System`, `Runtime`, `Math`, `String`,
  `StringBuffer`, `Thread`의 문서 메서드와 예외
- 배열은 독립적으로 clone하고 비-`Cloneable` 객체는
  `CloneNotSupportedException`을 발생시킨다.
- 종료된 `Thread`의 재시작을 `IllegalThreadStateException`으로 거부하며,
  worker continuation, sleep/yield, audio wait와 시작 상태를 save-state에 보존한다.
- `InputStream`/`OutputStream`, byte/data/character stream, EUC-KR과 UTF 계열,
  `DataInput`/`DataOutput` 동작
- `Vector`, `Stack`, `Hashtable`, `Enumeration`, `Random`, `Timer`,
  `TimerTask`, `Date`, `Calendar`, `TimeZone`
- 단말 설정의 분 단위 offset을 기본 시간대와 `Date.toString`에 반영한다.
- `Reference`와 `WeakReference`를 GC root 처리와 save-state 검증에 연결한다.
- CLDC `Connector`의 file/resource/socket/http 모델과 stream close 상태를
  구현했다.

## SKVM MIDP 2.0 구현 결과

### LCDUI와 game

- `Display`, `Displayable`, `Canvas`, `Screen`, `Form`, `List`, `Alert`,
  `TextBox`, `Item` 계층, `Command`, `ChoiceGroup`, `Gauge`, `TextField`,
  `Ticker`, `Font`, `Image`를 구현했다.
- command와 item listener, `callSerially`, repaint, key/pointer, 선택 및 text
  constraint가 실제 guest callback과 상태 변경을 수행한다.
- mutable/immutable 이미지 규칙, RGB 및 region 생성, font metric과 baseline,
  `CustomItem` 기본 callback을 구현했다.
- `GameCanvas`, `Layer`, `Sprite`, `TiledLayer`, `LayerManager`에 frame,
  transform, reference pixel, animated tile, pixel collision, key-state mask,
  z-order, view-window clip과 graphics-state 복원을 구현했다.

### 미디어

- `Manager`, `Player`, `PlayerListener`, `VolumeControl`, `ToneControl`과
  `MediaException`을 공용 media service에 연결했다.
- realize/prefetch/start/stop/deallocate/close, media time, duration, loop,
  control 조회, volume/mute 및 listener 이벤트를 구현했다.
- `ToneControl`은 VERSION/TEMPO/RESOLUTION, block 정의/재생, volume,
  repeat, note와 silence를 8 kHz PCM WAV로 렌더링한다.

### RMS, 네트워크, 보안과 MIDlet

- RMS metadata, record CRUD, listener, filter/comparator enumeration,
  keepUpdated/destroy, 정확한 예외와 save-state를 구현했다. 열린 store의 삭제는
  `RecordStoreException`으로 거부한다.
- HTTP/HTTPS URL 분해, request/response header, 상태/날짜, socket option,
  UDP datagram, server socket, serial `CommConnection`, `PushRegistry`를 구현했다.
- `SecurityInfo`, `Certificate`, `CertificateException`과 HTTPS의 결정론적
  인증서 모델을 추가했다.
- MIDlet lifecycle, `platformRequest`, permission 검사와
  `MIDletStateChangeException`을 구현했다.

## 현재 남아 있는 Java 차이와 의도적 제한

다음 항목은 메서드 누락이나 무조건 성공하는 스텁이 아니라, 실제 단말 밖에서
동작하는 에뮬레이터의 정책 또는 아직 없는 하위 장치/코덱이다.

### 외부 통신과 단말 하드웨어

- KTF `Socket`/`HttpSocket`과 SKVM 네트워크는 재현 가능한 로컬 모델이다.
  호스트 인터넷에 임의 접속하지 않으며 KTF offline HTTP는 503을 반환한다.
- HTTPS 인증서는 실제 원격 TLS handshake 결과가 아니라 결정론적 가상
  인증서다.
- WIPI 2 GPS callback은 가상 위치와 가상 시각을 사용한다. 실제 GPS/RF 센서,
  기지국 또는 PDE 연결은 없다.
- SMS와 주소록은 VM 소유의 로컬 상태다. 이동통신망 전송이나 OS 연락처를
  변경하지 않는다.
- 카메라는 실제 카메라 장치 대신 현재 에뮬레이터 framebuffer를 캡처한다.
- SKVM `Thread.join()`은 worker thread 안에서는 scheduler wait를 수행하지만,
  MIDlet 주 callback 자체는 보존 가능한 worker continuation이 아니므로 그
  callback에서 활성 worker를 join하는 경우 비동기 실행 모델의 제약이 남는다.

### 미디어 codec

공용 media service는 PCM WAV, Standard MIDI와 SMAF/MMF를 디코딩한다. SKVM
tone sequence는 이 브랜치에서 PCM으로 변환된다. MP3, AMR, QCP/QCELP, AAC와
동영상 codec은 아직 없으므로 해당 데이터의 클래스/메서드가 존재해도 실제
오디오·비디오 재생은 되지 않는다.

### UI 정밀도

위젯 상태, callback과 raster 동작은 구현됐지만 제조사별 font metric, soft-key
모양, native theme, annunciator 크기와 터치 장치 특성은 실제 KTF 단말과 픽셀
단위로 동일하지 않다. 이는 API 누락과 별개의 handset profile 정확도 문제다.

## WIPI-C 잔여 차이

이번 브랜치는 WIPI-C를 변경하지 않았다. 따라서 최초 비교 결과가 그대로 남아
있다.

### WIPI 1.2.1 정확 이름

현재 catalog는 239개 항목이며 그중 `MC_*`는 208개다. 위키의 서로 다른
`MC_*` 함수 제목 211개와 정확 이름으로 비교하면 다음 8개가 다르다.

| 위키 이름 | 현재 상태 |
|---|---|
| `MC_imGetCurrentMode` | 없음 |
| `MC_imGetSupportedModes` | 없음 |
| `MC_imGetSurpportModeCount` | 없음; 위키 오탈자 포함 이름 |
| `MC_imHandleInput` | 없음 |
| `MC_imSetCurrentMode` | 없음 |
| `MC_grpFillPolygon` | `MC_grpDrawFillPolygon` 이름으로 구현 |
| `MC_knlDestroyShareBuf` | `MC_knlDestroySharedBuf` 이름으로 구현 |
| `MC_knlGeAppManagerID` | `MC_knlGetAppManagerID` 이름으로 구현 |

catalog에는 반대로 위키 1.2.1 제목 집합에 없는 `MC_knlDefTimer`와
`MC_mdaRecord`가 있다.

### WIPI 2.x

- WIPI 2.0 문서 이름 중 113개가 없고, 선택 사항 VGI 25개를 제외하면 88개다.
- WIPI 2.2 문서 이름 중 104개가 없고, VGI를 제외하면 79개다.
- 주요 그룹은 terminal resource, generic I/O, SSL, GPS/location, SMS,
  kernel/media/math 확장과 InputMethod/graphics 확장이다.

Java 쪽에 해당 기능이 구현됐다고 해서 WIPI-C symbol이 자동으로 생기지는 않는다.
WIPI-C 2.x를 목표로 할 경우 공용 service를 재사용하되 ABI catalog, parameter
marshalling과 C callback을 별도로 추가해야 한다.

## 검증

이 브랜치에서 다음을 확인했다.

- `go test ./...` 전체 통과
- KTF 1.2.1 core/I/O/event/IME/LWC/media/call 회귀 테스트
- KTF 2.x class hierarchy, IODevice, SMS, ResourceGroup, address/GPS/camera,
  media control 및 save-state 테스트
- SKVM native registry golden 갱신과 CLDC/MIDP descriptor 감사
- SKVM CLDC core/util/I/O/reference, LCDUI/game/media/RMS/network/MIDlet 및
  save-state/GC 테스트

## 구현 위치

- KTF 1.2.1 host 선언: [`ktf_java_specs.go`](../application/internal/ktf/ktf_java_specs.go)
- KTF core 및 dispatch: [`ktf_java_native.go`](../application/internal/ktf/ktf_java_native.go)
- KTF event/IME: [`ktf_java_jlet.go`](../application/internal/ktf/ktf_java_jlet.go)
- KTF LWC: [`ktf_lwc.go`](../application/internal/ktf/ktf_lwc.go)
- KTF 2.x 선언/I/O/SMS/resource: [`ktf_java_wipi2.go`](../application/internal/ktf/ktf_java_wipi2.go)
- KTF 2.x address/GPS: [`ktf_java_wipi2_handset.go`](../application/internal/ktf/ktf_java_wipi2_handset.go)
- KTF 2.x media/camera: [`ktf_java_wipi2_media.go`](../application/internal/ktf/ktf_java_wipi2_media.go)
- SKVM host class와 실행 조정: [`vm.go`](../skvm/vm.go)
- SKVM CLDC core: [`natives_cldc_core_extra.go`](../skvm/natives_cldc_core_extra.go)
- SKVM CLDC I/O/util: [`natives_cldc_io.go`](../skvm/natives_cldc_io.go), [`natives_cldc_util_extra.go`](../skvm/natives_cldc_util_extra.go)
- SKVM MIDP LCDUI/game: [`natives_midp_lcdui.go`](../skvm/natives_midp_lcdui.go), [`natives_midp_game.go`](../skvm/natives_midp_game.go)
- SKVM MIDP media/RMS/network: [`natives_midp_media.go`](../skvm/natives_midp_media.go), [`natives_midp_rms.go`](../skvm/natives_midp_rms.go), [`natives_midp_network.go`](../skvm/natives_midp_network.go)
- WIPI-C catalog: [`catalog_gen.go`](../wipi/catalog_gen.go)
