package application

import (
	"fmt"
	"image"
	"testing"
)

func TestIssue530PlayMenuFooter(t *testing.T) {
	path, data := findAuthorizedPackage(t, "1e7753773e3afb794d704bc67f3e70bc58336bfd28cb762094da97e057f8914f")
	m := newBREWReferenceMachine(t, path, data)
	queueBREWReportedInputs(t, m, []brewReportedKeyHold{
		{"ok", 5616, 5696}, {"ok", 6112, 6208}, {"ok", 6544, 6624}, {"ok", 7088, 7184},
		{"right", 8016, 8128}, {"ok", 8336, 8416}, {"ok", 8832, 8928}, {"ok", 9952, 10032},
		{"hash", 11424, 11520}, {"ok", 12368, 12480}, {"back", 12976, 13088},
		{"ok", 13424, 13536}, {"ok", 14560, 14656}, {"hash", 17200, 17280},
	})
	stepBREWReference(t, m, 1085)
	footer := m.Framebuffer().(*image.RGBA).SubImage(image.Rect(0, 123, 120, 136))
	if got, want := fmt.Sprintf("%x", brewFrameHash(footer)), "e2fad4b76fbc6f3f542c46406a93c13851d20ccf105c63efd4b9c40855ad029d"; got != want {
		t.Fatalf("play menu footer hash = %s, want %s", got, want)
	}
}
