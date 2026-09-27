package runtime

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

var issue350PCMReference = map[string]struct {
	frames int
	hash   string
}{
	"sound/s01.mmf": {2246453, "91b922177bc596266785dbe180653fe8adfc6fdc9bf099a7248175ccd5a2fa1f"},
	"sound/s02.mmf": {101077, "7eab57cf2cf02e898c914d23bfaff9099e2c2db95878ffea982b9f6df136ddbb"},
	"sound/s03.mmf": {143413, "eb2a836003964bc9c600742f6dfa3cbf68956e031df02da0b5f5f978e334a964"},
	"sound/s04.mmf": {107427, "4748fa9f679ad0cf8023273c5b82c19bfc694e1652fb625f6beeedf5fe3c4eca"},
	"sound/s05.mmf": {112014, "2e7d302a0887205d39c7317c1fa14c16220d823226b9ee6887d38f1dd90b93f8"},
	"sound/s06.mmf": {101253, "f33cb806146baaeb784f68ddd6cf4140f563e7b9243a913fa806865eeedebc63"},
	"sound/s07.mmf": {170049, "60d9b4be2e4e8c8d7c23127747dfe52909f8d3a22ace6dd388fa4632f3c8b3bd"},
	"sound/s08.mmf": {152056, "37689f83c65d53e2d23e940d7d1339febf937ac01429252b47a36da49bad276f"},
	"sound/s09.mmf": {1582484, "4a95bedd44060ddb1c2a394de418d29a9abb56e91062a622438c438869883998"},
	"sound/s10.mmf": {121715, "1bee1078fe4374cb64d08e0732bd158314fdcae9b28ddebd638b7341e1315d5e"},
	"sound/s11.mmf": {121362, "dc14728e3db28280b2ee6a0dddbbdafc377b0fcb8e9bac687df602aa763b6e64"},
	"sound/s12.mmf": {97725, "979fcd90bdd4569fd3ba8c0a34ca05dbda7707dfa673c83cf94d5e90bb252665"},
}

// The exact local #350 archive is optional: no authored game data is checked in.
func TestSMAFIssue350Reference(t *testing.T) {
	path := os.Getenv("ARAM_SMAF_ARCHIVE")
	if path == "" {
		t.Skip("ARAM_SMAF_ARCHIVE is not set")
	}
	archive, err := os.ReadFile(path)
	check(t, err)
	if fmt.Sprintf("%x", sha256.Sum256(archive)) != "2d68eabc01fef9d7828558fe477d232c8b242f88e14fab215438450abd18d00c" {
		t.Skip("ARAM_SMAF_ARCHIVE is not the exact #350 archive")
	}
	outer, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	check(t, err)
	var jarData []byte
	for _, entry := range outer.File {
		if strings.HasSuffix(strings.ToLower(entry.Name), ".jar") {
			jarData = readIssue350Entry(t, entry)
			break
		}
	}
	jar, err := zip.NewReader(bytes.NewReader(jarData), int64(len(jarData)))
	check(t, err)
	clips, totalMisroutes, totalOldHeld := 0, 0, 0
	for _, entry := range jar.File {
		if !strings.HasSuffix(strings.ToLower(entry.Name), ".mmf") {
			continue
		}
		t.Run(entry.Name, func(t *testing.T) {
			data := readIssue350Entry(t, entry)
			fixed, legacy := issue350Decoder(t, data), issue350Decoder(t, data)
			cursor, misroutes, duplicates := uint64(0), 0, 0
			for _, event := range fixed.events {
				if (event.kind == smafNoteOn || event.kind == smafNoteOff) && event.noteID == 0 {
					t.Fatal("note lost its gate identity")
				}
				for cursor < event.sample {
					advanceIssue350Envelopes(fixed)
					advanceIssue350Envelopes(legacy)
					cursor++
				}
				fixed.fire(event)
				if event.kind != smafNoteOff {
					legacy.fire(event)
					continue
				}
				// Reproduce the pre-fix pitch-only lookup. Key-down bookkeeping
				// is observational here; it does not affect synthesis/envelopes.
				for i := range legacy.pool {
					voice := &legacy.pool[i]
					if voice.active && voice.channel == event.channel && voice.keyNote == event.a {
						if voice.noteID != event.noteID {
							misroutes++
						}
						if !voice.keyDown {
							duplicates++
						}
						voice.noteOff()
						break
					}
				}
			}
			oldHeld := 0
			for i := range fixed.pool {
				if fixed.pool[i].active && fixed.pool[i].keyDown {
					t.Fatalf("note %d is still held after its gate", fixed.pool[i].noteID)
				}
				if legacy.pool[i].active && legacy.pool[i].keyDown {
					oldHeld++
				}
			}
			totalMisroutes += misroutes
			totalOldHeld += oldHeld
			// Audit production render/probe and the incremental playback path,
			// not just the event simulation above.
			eager := decodeSMAFPCM16(data, 44_100)
			lazy := decodeSMAFLazyPCM16(data, 44_100)
			if eager == nil || lazy == nil || len(eager.samples) == 0 {
				t.Fatal("no PCM")
			}
			for frame := 734; frame < len(eager.samples)/2; frame += 735 {
				lazy.ensureFrame(uint64(frame))
			}
			lazy.ensureFrame(uint64(len(eager.samples)/2 - 1))
			if eager.duration != lazy.duration || !slices.Equal(eager.samples, lazy.samples) {
				t.Fatal("incremental PCM or duration differs from full render")
			}
			probe := newSMAFRenderStream(issue350Decoder(t, data)).probeEnd()
			if probe != uint64(len(eager.samples)/2) {
				t.Fatal("silent probe ended on a different sample")
			}
			raw := make([]byte, len(eager.samples)*2)
			for i, sample := range eager.samples {
				binary.LittleEndian.PutUint16(raw[i*2:], uint16(sample))
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(raw))
			want, found := issue350PCMReference[entry.Name]
			if !found || len(eager.samples)/2 != want.frames || hash != want.hash {
				t.Fatalf("PCM frames=%d hash=%s; want %+v", len(eager.samples)/2, hash, want)
			}
			t.Logf("events=%d misrouted-before=%d duplicate-offs-before=%d held-before=%d held-after=0 PCM=%x frames=%d", len(fixed.events), misroutes, duplicates, oldHeld, sha256.Sum256(raw), len(eager.samples)/2)
			clips++
		})
	}
	if clips != 12 || totalMisroutes != 443 || totalOldHeld != 92 {
		t.Fatalf("clips=%d old misroutes=%d old held=%d; expected exact reproducer", clips, totalMisroutes, totalOldHeld)
	}
	t.Logf("12 clips: %d misrouted gates before fix, %d unreleased keys before fix, none after", totalMisroutes, totalOldHeld)
}

func readIssue350Entry(t *testing.T, entry *zip.File) []byte {
	t.Helper()
	r, err := entry.Open()
	check(t, err)
	defer r.Close()
	data, err := io.ReadAll(r)
	check(t, err)
	return data
}

func issue350Decoder(t *testing.T, data []byte) *smafDecoder {
	t.Helper()
	d := &smafDecoder{rate: 44_100}
	if !d.parse(data) || !d.buildEvents() {
		t.Fatal("could not parse exact score")
	}
	return d
}

func advanceIssue350Envelopes(d *smafDecoder) {
	for i := range d.pool {
		if d.pool[i].active {
			d.pool[i].tick(true)
		}
	}
}
