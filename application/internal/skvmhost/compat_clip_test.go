package skvmhost

import (
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
)

func TestMonsterBoyCompatibilityRequiresExactIdentity(t *testing.T) {
	for _, field := range []string{"match", "digest", "main", "program", "width", "height"} {
		t.Run(field, func(t *testing.T) {
			source := machinecore.Source{SHA256: "c6cadf75c454638e2c14f7549a2062e7c37680c1eaf9536ec0d4bfb20acfe3c2"}
			var pkg skloader.Package
			pkg.Descriptor.MainClass = "Game"
			pkg.Descriptor.ProgramName = "0052335225"
			geometry := image.Pt(240, 320)
			switch field {
			case "digest":
				source.SHA256 = "different"
			case "main":
				pkg.Descriptor.MainClass = "Other"
			case "program":
				pkg.Descriptor.ProgramName = "other"
			case "width":
				geometry.X++
			case "height":
				geometry.Y++
			}
			config := shared.DefaultConfig()
			applySKVMTitleCompatibility(&config, source, pkg, geometry)
			// Exercise the real service boundary, which rejects unordered or
			// duplicate quirks even when a name-only lookup appears correct.
			if _, err := shared.NewServices(config); err != nil {
				t.Fatalf("initialize title services: %v", err)
			}
			for _, name := range []string{skengine.InclusiveSetClipQuirk, skengine.CanvasHeightInset16Quirk} {
				enabled := false
				for _, q := range config.Device.Quirks {
					if q.Name == name && q.Enabled {
						enabled = true
					}
				}
				if enabled != (field == "match") {
					t.Fatalf("quirk %s=%t for %s", name, enabled, field)
				}
			}
			if got := skvmTitleCanvas(source, pkg, geometry); got != geometry {
				t.Fatalf("unexpected geometry override: %v", got)
			}
		})
	}
}
