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

// Updated after sequence-end timing was corrected. PCM through each former
// endpoint was verified unchanged; these hashes also cover the retained rest
// and filter tail through the exact current endpoint.
var issue350PCMReference = map[string]struct {
	frames int
	hash   string
}{
	"sound/s01.mmf": {2246806, "197247c7587364deb12c27fa3635b2b61dacc38db6d5780a2a9b5939daf05d3d"},
	"sound/s02.mmf": {101430, "9461078e968afb8fb2604e2151a9193f598e7cec0d9353aea7030c42dc7ba81f"},
	"sound/s03.mmf": {143766, "5bb8ef415caa5a5a38fb40f54574dd60ca8189b3920a0786353ffbd1eeb1ddc5"},
	"sound/s04.mmf": {107780, "71a4fba207a70a6e1ce83bacac75385536682453b8feb1a2c113e8d583343c65"},
	"sound/s05.mmf": {112366, "4df8adcbf09daa95bd65557aea2ab97da807fecaac77dbc9239743ab85116fb2"},
	"sound/s06.mmf": {101606, "a7f24b3bd1e5aa914398a9a500cdbd25d5752e966df873194576daf786f0cbfa"},
	"sound/s07.mmf": {170402, "7370bf791e4860ccc3329bc714ebd1c5ab3e9d56a2e5dd14838067a43d5e8419"},
	"sound/s08.mmf": {152409, "10a4bbe41aa6803ce1e230dc65b9aabd85b604aed5af85f1ce15aafa7f5a1dca"},
	"sound/s09.mmf": {1582837, "7af5049513effd15830f4fe19398ec105a7ce0b3c97dedd44967279778b2d9cf"},
	"sound/s10.mmf": {122068, "37f2e3b79182e96af06a1b8363ad79827f63b311dba489bf6e94e080ff8dd1a5"},
	"sound/s11.mmf": {121716, "6c40562cee8ba6c2c7b1da0a6a8871abd7f689852aedef59fef19e0405211164"},
	"sound/s12.mmf": {98078, "ab04be6eaa7522163a9ec2012134b9036180bb76d1208685e6ec1d3c1b15f1d2"},
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
