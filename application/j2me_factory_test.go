package application

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/brew"
	"github.com/mirusu400/aram-core/loader/j2me"
)

func zipFilesForJ2METest(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		payload, e := io.ReadAll(r)
		_ = r.Close()
		if e != nil {
			return nil, e
		}
		files[f.Name] = payload
	}
	return files, nil
}

func syntheticJ2MEFiles(t *testing.T) map[string][]byte {
	return map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\nMIDlet-1: Demo, , Game\nMIDlet-Name: Demo\n"),
		"Game.class":           syntheticSKVMLifecycleClass(t), "resource.txt": []byte("synthetic")}
}
func TestJ2MEFactoryLifecycleProfilesAndState(t *testing.T) {
	jar := syntheticSKVMZIP(t, syntheticJ2MEFiles(t))
	pair := syntheticSKVMZIP(t, map[string][]byte{"Game.jar": jar, "Game.jad": []byte("MIDlet-1: Demo, , Game\n")})
	for _, data := range [][]byte{jar, pair} {
		for _, profile := range []string{j2me.ProfileID, j2me.LGTProfileID} {
			source := machinecore.Source{Name: "synthetic.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data)), ProfileID: profile}
			created, err := NewFactory().Create(context.Background(), source)
			check(t, err)
			machine := created.(*skvmhost.Machine)
			t.Cleanup(func() { _ = machine.Close() })
			if info := machine.SourceInfo(); info.Format != "j2me" || info.ProfileID != profile || info.SHA256 == "" {
				t.Fatalf("wrong identity %+v", info)
			}
			carrier := "unknown"
			if profile == j2me.LGTProfileID {
				carrier = "lgt"
			}
			if machine.Services().Config.Device.Carrier != carrier {
				t.Fatal("wrong carrier")
			}
			if machine.DebugSnapshot(1).Runtime != "j2me" {
				t.Fatal("wrong runtime")
			}
			var ready bytes.Buffer
			check(t, machine.SaveState(&ready))
			if !bytes.HasPrefix(ready.Bytes(), []byte("ARAMJ2M\x00")) {
				t.Fatal("J2ME uses SKT state magic")
			}
			check(t, machine.Start(context.Background()))
			check(t, machine.QueueInput(machinecore.InputEvent{Control: "up", Pressed: true, At: time.Millisecond}))
			check(t, machine.StepFrame(context.Background()))
			var saved bytes.Buffer
			check(t, machine.SaveState(&saved))
			check(t, machine.StepFrame(context.Background()))
			var advanced bytes.Buffer
			check(t, machine.SaveState(&advanced))
			check(t, machine.LoadState(bytes.NewReader(saved.Bytes())))
			check(t, machine.StepFrame(context.Background()))
			var replay bytes.Buffer
			check(t, machine.SaveState(&replay))
			if !bytes.Equal(advanced.Bytes(), replay.Bytes()) {
				t.Fatal("state replay differs")
			}
			check(t, machine.Stop())
			check(t, machine.Reset(context.Background()))
			check(t, machine.LoadState(bytes.NewReader(ready.Bytes())))
			otherSource := source
			otherSource.ProfileID = j2me.LGTProfileID
			if profile == j2me.LGTProfileID {
				otherSource.ProfileID = j2me.ProfileID
			}
			other, err := NewFactory().Create(context.Background(), otherSource)
			check(t, err)
			t.Cleanup(func() { _ = other.Close() })
			if err = other.LoadState(bytes.NewReader(saved.Bytes())); err == nil {
				t.Fatal("cross-profile state accepted")
			}
			if other.State() != machinecore.StateReady {
				t.Fatal("failed state import mutated lifecycle")
			}
		}
	}
}
func TestJ2MEFactoryRejectsWrongHashProfileAndMalformedClass(t *testing.T) {
	data := syntheticSKVMZIP(t, syntheticJ2MEFiles(t))
	for _, source := range []machinecore.Source{
		{Name: "hash.jar", SHA256: strings.Repeat("0", 64), ReaderAt: bytes.NewReader(data), Size: int64(len(data))},
		{Name: "profile.jar", ProfileID: "wipi-1.2.1/skt/generic", ReaderAt: bytes.NewReader(data), Size: int64(len(data))},
	} {
		if machine, err := NewFactory().Create(context.Background(), source); err == nil {
			_ = machine.Close()
			t.Fatal("invalid Java source accepted")
		}
	}
	files := syntheticJ2MEFiles(t)
	files["Game.class"] = []byte("bad")
	data = syntheticSKVMZIP(t, files)
	if _, err := NewFactory().Create(context.Background(), machinecore.Source{Name: "bad.jar", ReaderAt: bytes.NewReader(data), Size: int64(len(data))}); err == nil || !strings.Contains(err.Error(), "J2ME") {
		t.Fatalf("malformed class diagnosis: %v", err)
	}
}

func TestJ2MEExternalRMSIsUnsupportedSource(t *testing.T) {
	jar := syntheticSKVMZIP(t, syntheticJ2MEFiles(t))
	data := syntheticSKVMZIP(t, map[string][]byte{"Game.jar": jar, "Game.jad": []byte("MIDlet-1: Demo, , Game\n"), "saved.idx": []byte("synthetic")})
	_, err := NewFactory().Create(context.Background(), machinecore.Source{Name: "external-rms.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
	if !errors.Is(err, ErrUnsupportedSource) || !errors.Is(err, j2me.ErrUnsupportedFeature) {
		t.Fatalf("external RMS error=%v", err)
	}
}
func TestJ2MEFallbackPreservesExistingDetectionPriority(t *testing.T) {
	mif := make([]byte, 64)
	for off, value := range map[int]uint32{0: brew.MIFTag, 4: 0x10001, 8: 32, 12: 8, 16: 40, 20: 1, 24: 48, 28: 16} {
		binary.LittleEndian.PutUint32(mif[off:], value)
	}
	cases := map[string]map[string][]byte{
		"BREW missing MOD":          {"title.mif": mif},
		"BREW truncated MIF":        {"title.mif": mif[:8], "title.mod": []byte("module")},
		"GNEX malformed paired SGS": {"title.sgs": []byte("bad"), "title.inf": []byte("descriptor")},
		"Android":                   {"AndroidManifest.xml": []byte("binary xml"), "classes.dex": []byte("dex 035")},
		"GNEX":                      {"title.sgs": syntheticGNEXSGS(), "title.inf": []byte("descriptor")},
		"BREW":                      {"title.mif": mif, "title.mod": []byte("opaque module")},
		"KTF":                       {"__adf__": []byte("PID:PD000001\nAID:01020304\nMClass:GameMain\n"), "01020304.jar": syntheticSKVMZIP(t, map[string][]byte{"client.bin4096": syntheticKTFClient()})},
		"Raptor diagnostic":         {"app_info": []byte("AID:missing\n")},
	}
	for label, extra := range cases {
		t.Run(label, func(t *testing.T) {
			files := syntheticJ2MEFiles(t)
			for k, v := range extra {
				files[k] = v
			}
			data := syntheticSKVMZIP(t, files)
			created, err := NewFactory().Create(context.Background(), machinecore.Source{Name: "collision.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
			if created != nil {
				defer created.Close()
				if java, ok := created.(*skvmhost.Machine); ok && java.SourceInfo().Format == "j2me" {
					t.Fatal("J2ME stole existing format")
				}
			}
			if err != nil && strings.Contains(err.Error(), "J2ME") {
				t.Fatalf("J2ME stole existing diagnosis: %v", err)
			}
		})
	}
	// A valid SKVM quartet outranks a standalone MIDlet declaration in the same ZIP.
	skt := syntheticSKVMPackage(t)
	zr, err := zipFilesForJ2METest(skt)
	check(t, err)
	for k, v := range syntheticJ2MEFiles(t) {
		zr[k] = v
	}
	data := syntheticSKVMZIP(t, zr)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{Name: "collision.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
	check(t, err)
	defer created.Close()
	if created.(*skvmhost.Machine).SourceInfo().Format != "skvm" {
		t.Fatal("J2ME stole SKVM package")
	}
}
