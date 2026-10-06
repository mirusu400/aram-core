package application

import (
	"bytes"
	"context"
	"image"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
)

func TestJ2MEFactoryAppliesGuestWidthToRuntime(t *testing.T) {
	archive := syntheticSKVMZIP(t, syntheticJ2MEFiles(t))
	for _, test := range []struct {
		name     string
		override int
		want     int
	}{
		{name: "native", want: 240},
		{name: "wide", override: 640, want: 640},
		{name: "capped", override: 5000, want: 4096},
	} {
		t.Run(test.name, func(t *testing.T) {
			factory := NewFactory()
			factory.GuestWidthOverride = test.override
			created, err := factory.Create(context.Background(), machinecore.Source{
				Name: "synthetic.jar", ReaderAt: bytes.NewReader(archive), Size: int64(len(archive)),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer created.Close()
			assertJavaGuestWidth(t, created.(*skvmhost.Machine), test.want, 320)
		})
	}
}

func TestSKVMFactoryAppliesGuestWidthToRuntime(t *testing.T) {
	const digest = "08aa799c11b0d97d4f3b03fc1c2ce7fb28136009227ae6442e0d5ca105fe6ede"
	path, archive := findAuthorizedPackage(t, digest)
	for _, test := range []struct {
		name     string
		override int
		want     int
	}{
		{name: "native", want: 240},
		{name: "wide", override: 640, want: 640},
	} {
		t.Run(test.name, func(t *testing.T) {
			factory := NewFactory()
			factory.GuestWidthOverride = test.override
			created, err := factory.Create(context.Background(), machinecore.Source{
				Name: filepath.Base(path), Path: path,
				ReaderAt: bytes.NewReader(archive), Size: int64(len(archive)),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer created.Close()
			assertJavaGuestWidth(t, created.(*skvmhost.Machine), test.want, 320)
		})
	}
}

func assertJavaGuestWidth(t *testing.T, machine *skvmhost.Machine, width, height int) {
	t.Helper()
	if got := machine.Services().Config.Device.ScreenWidth; got != int32(width) {
		t.Fatalf("guest screen width = %d, want %d", got, width)
	}
	if got := machine.Framebuffer().Bounds(); got != image.Rect(0, 0, width, height) {
		t.Fatalf("framebuffer = %v, want %dx%d", got, width, height)
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatalf("first guest frame: %v", err)
	}
}
