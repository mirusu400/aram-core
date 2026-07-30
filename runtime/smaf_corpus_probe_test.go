package runtime

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSMAFOptionalCorpusProbe(t *testing.T) {
	path := os.Getenv("ARAM_SMAF_ARCHIVE")
	if path == "" {
		t.Skip("ARAM_SMAF_ARCHIVE is not set")
	}
	outer, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Close()
	var jarBytes []byte
	for _, entry := range outer.File {
		if strings.HasSuffix(strings.ToLower(entry.Name), ".jar") {
			reader, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			jarBytes, err = io.ReadAll(reader)
			reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	jar, err := zip.NewReader(bytes.NewReader(jarBytes), int64(len(jarBytes)))
	if err != nil {
		t.Fatal(err)
	}
	decodedCount := 0
	for _, entry := range jar.File {
		if !strings.HasSuffix(strings.ToLower(entry.Name), ".mmf") {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		decoded := decodeSMAFPCM16(data, 44_100)
		elapsed := time.Since(started)
		if decoded == nil {
			t.Errorf("%s: decode returned nil", entry.Name)
			continue
		}
		var peak int16
		for _, sample := range decoded.samples {
			absolute := sample
			if absolute < 0 {
				absolute = -absolute
			}
			if absolute > peak {
				peak = absolute
			}
		}
		if peak < 100 {
			t.Errorf("%s: silent peak %d", entry.Name, peak)
			continue
		}
		if strings.Contains(entry.Name, "BGM_TITLE") {
			lazyStarted := time.Now()
			lazy := decodeSMAFLazyPCM16(data, 44_100)
			lazyOpen := time.Since(lazyStarted)
			if lazy == nil {
				t.Fatal("lazy title decode returned nil")
			}
			frameStarted := time.Now()
			lazy.ensureFrame(734)
			t.Logf(
				"%s lazy: open=%s first-frame=%s cached=%d",
				entry.Name,
				lazyOpen,
				time.Since(frameStarted),
				len(lazy.samples)/2,
			)
		}
		eventEnd := time.Duration(0)
		probe := &smafDecoder{rate: 44_100}
		if probe.parse(data) && probe.buildEvents() && len(probe.events) != 0 {
			eventEnd = time.Duration(
				probe.events[len(probe.events)-1].sample *
					uint64(time.Second) / 44_100,
			)
		}
		t.Logf(
			"%s: %s events=%s tail=%s peak=%d decode=%s",
			entry.Name,
			decoded.duration,
			eventEnd,
			decoded.duration-eventEnd,
			peak,
			elapsed,
		)
		decodedCount++
	}
	if decodedCount == 0 {
		t.Fatal("no SMAF clips decoded")
	}
}
