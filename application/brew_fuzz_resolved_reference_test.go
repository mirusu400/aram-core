package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Replays resolved fuzz reports against exact authorized archives. The private
// title bytes remain outside the repository.
func TestBREWResolvedFuzzReportsReference(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	controls := []string{
		"up", "down", "left", "right", "ok", "soft-left", "soft-right", "menu",
		"back", "send", "end", "star", "hash", "num0", "num1", "num2", "num3",
		"num4", "num5", "num6", "num7", "num8", "num9",
	}
	for _, test := range []struct {
		issue                         int
		category                      string
		name                          string
		digest                        string
		seed                          int64
		reportedFrame, reportedInputs int
	}{
		{387, "기타", "[KTF BREW] 동전쌓기2(작화).zip", "8dcf9ad11776f010c1e7ac0bd33b13a2d1df9f8af075a057d0d2ae92ed57a9eb", 0, 77, 0},
		{388, "레이싱", "[KTF BREW] 드림랠리(작화).zip", "a99c50008ae00b9c71915ee3c708ccd3cc4fe895c1932b22e58540df983d9b87", 102, 492, 139},
		{389, "레이싱", "[KTF BREW] 미니카레이싱 GP+.zip", "85c56cc7f37253b673fcd1b28ac3ab7cfcb3362810e56a1af6acb0d2450b2b8e", 102, 408, 114},
		{393, "시뮬레이션", "[KTF BREW] 센티멘탈러브+.zip", "b6f9e615e605bf6d4c0d4d87041274dd86b728937489f99a08090dd4912006ed", 102, 428, 118},
		{394, "액션", "[KTF BREW] 무한의 룩(작화).zip", "72f7036a3718681ca54677e5de5645d1f793f1e897543b49442a360480e27837", 102, 18, 4},
		{395, "액션", "[KTF BREW] 스플린터셀.zip", "fd1a7166aec3023d6a51d88b39cafee2c212204fbce8e9a8420258d89f488dfd", 0, 68, 0},
		{396, "액션", "[KTF BREW] 열혈강호(작화).zip", "c926a78391953237fbb805d0d1cd381d846b332b14ed1932b3cc6b1d94ac1941", 0, 5, 0},
		{398, "액션", "[KTF BREW] 콤보벽돌깨기.zip", "c0b32bd6612738695f82e506d66cfa49ad39193b5f18236a4df25a186aacbe1a", 102, 100, 30},
		{400, "전략-SRPG", "[KTF BREW] 커맨드 앤 컨트롤.zip", "ea2f2185f91a53cef0518577624ab0fe215b0ee65f24ca980b08be0518ace086", 0, 303, 0},
		{404, "퍼즐-카드", "[KTF BREW] 맞짱맞고(작화).zip", "f5374d6bad1bf557d2fa58706c8b8d0f3a811c287dcbadf0a289cee86e213453", 102, 389, 108},
	} {
		t.Run(fmt.Sprint(test.issue), func(t *testing.T) {
			path := filepath.Join(root, "KTF BREW 게임파일", test.category, test.name)
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				t.Skip("exact archive is not present in the authorized corpus")
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != test.digest {
				t.Fatalf("archive SHA-256=%s, want %s", got, test.digest)
			}
			factory := NewFactory()
			factory.AllowUntrustedBREW = true
			machine, err := factory.Create(context.Background(), machinecore.Source{
				Name: test.name, SHA256: test.digest, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer machine.Close()
			if err := machine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			random := rand.New(rand.NewSource(test.seed))
			held := make(map[string]bool)
			presses := 0
			for frame := 0; frame < 600; frame++ {
				if test.seed != 0 && random.Intn(4) == 0 {
					control := controls[random.Intn(len(controls))]
					pressed := !held[control]
					if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: pressed}); err != nil {
						t.Fatal(err)
					}
					held[control] = pressed
					presses++
				}
				if err := machine.StepFrame(context.Background()); err != nil {
					t.Fatalf("seed=%d frame=%d presses=%d: %v", test.seed, frame, presses, err)
				}
				if frame == test.reportedFrame && presses != test.reportedInputs {
					t.Fatalf("reported frame %d has %d events, want %d", frame, presses, test.reportedInputs)
				}
			}
			if machine.State() != machinecore.StateRunning {
				t.Fatalf("state=%s after 600 frames, want running", machine.State())
			}
			brew, ok := machine.(*brewMachine)
			if !ok {
				t.Fatalf("exact BREW archive selected %T", machine)
			}
			stats, available := brew.BREWFrameStats()
			if !available || stats.PresentCount == 0 || !stats.FrameValid {
				t.Fatalf("no guest rendering evidence: stats=%+v available=%v", stats, available)
			}
			t.Logf("completed 600 frames with %d events and %d guest presentations", presses, stats.PresentCount)
		})
	}
}
