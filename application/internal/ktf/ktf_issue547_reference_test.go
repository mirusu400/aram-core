package ktf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	ktfloader "github.com/mirusu400/aram-core/loader/ktf"
	"github.com/mirusu400/aram-core/profile"
)

// The WIPI-C key events a native Clet's handleCletEvent receives.
const (
	ktfNativeKeyPress   = uint32(2)
	ktfNativeKeyRelease = uint32(3)
	ktfNativeKeyRepeat  = uint32(4)
)

// TestIssue547CletKeyEdgesReachNativeCodeAsHandsetEvents runs each title's
// own compiled Clet$CletCard.keyNotify and records what it hands the native
// Clet.handleInput. A press has to arrive as the native press event and a
// release as the native release event; the two used to be swapped for every
// Clet except a per-title list, so 크로노스윙 (issue #547) and SBS포트리스4
// (issue #570) both stood still while a direction was held and moved on their
// own once it was released. 물가에돌튕기기3 is the title the swap was first
// introduced for (issue #237); its facade forwards the same values. The SDK
// compiled the facade in three instruction shapes; 드래곤로드 covers the one
// none of the others use.
func TestIssue547CletKeyEdgesReachNativeCodeAsHandsetEvents(t *testing.T) {
	for _, test := range []struct {
		name   string
		digest string
	}{
		{"크로노스윙 issue 547", "1fe6c1c897d708dbb587a083dc450727c12de152de5e9e6458af75294a3061bb"},
		{"물가에돌튕기기3 issue 237", "6350b347278915217c9a4c56d82970bcf0ff1c686542c6cf9aff38ec42a7eb3d"},
		{"SBS포트리스4 issue 570", "7618e9a8f50aa8fc6b50f1e49e9ad3641618e1b41d3ef921efe3d63f120ac8db"},
		{"드래곤로드", "4b8c8f5ff7d61e1964eb8784d60997bcf977050a9df96cf853a9643109fec7ce"},
	} {
		t.Run(test.name, func(t *testing.T) {
			pkg := findAuthorizedIssue547Package(t, test.digest)
			r, err := NewRuntime(interpreter.New(), pkg)
			check(t, err)
			t.Cleanup(func() { _ = r.CPU.Close() })
			r.DeferThreads = true
			check(t, r.MapImageAndHost())
			ctx := context.Background()
			_, _, err = r.Bootstrap(ctx)
			check(t, err)
			check(t, r.Initialize(ctx))
			check(t, r.StartMainClass(ctx))

			step := func() {
				t.Helper()
				r.TickMS += 10
				check(t, r.Services.Clock.Advance(10*time.Millisecond))
				for slice := 0; slice < 32 && r.HasRunnableTask(); slice++ {
					check(t, r.RunTaskSlice(ctx, 100000).Err)
				}
			}
			for range 300 {
				if r.DefaultDisplay != 0 && r.DisplayCards[r.DefaultDisplay] != 0 {
					break
				}
				step()
			}
			card := r.DisplayCards[r.DefaultDisplay]
			if card == 0 {
				t.Fatal("no Clet card reached the display")
			}
			cardWords, err := r.ReadWords(card, 2)
			check(t, err)
			cardClass, err := r.InspectJavaClass(cardWords[1])
			check(t, err)
			if cardClass.Name != "Clet$CletCard" {
				t.Fatalf("displayed card class = %q, want Clet$CletCard", cardClass.Name)
			}

			// Route the title's native Clet.handleInput(III)V to a recorder,
			// so its own facade runs but none of its game code does.
			var events, keys []uint32
			recorder := r.RegisterHostCall("test.clet.handleInput", func(context.Context, *Runtime) (uint32, error) {
				parameters, err := r.ReadWords(r.NativeParameterBase, 3)
				if err != nil {
					return 0, err
				}
				events = append(events, parameters[1])
				keys = append(keys, parameters[2])
				return 0, nil
			})
			linked := false
			for _, classAddress := range r.JavaClasses {
				class, err := r.InspectJavaClass(classAddress)
				if err != nil || class.Name != "Clet" {
					continue
				}
				for _, method := range class.Methods {
					if method.Name == "handleInput" && method.Descriptor == "(III)V" {
						check(t, r.writeWords(method.Address+8, []uint32{recorder}))
						linked = true
					}
				}
			}
			if !linked {
				t.Fatal("title has no native Clet.handleInput(III)V")
			}

			key := int32(profile.KeyLeft)
			for _, edge := range []struct {
				name   string
				queue  func() (bool, error)
				native uint32
			}{
				{"press", func() (bool, error) { return r.QueueKeyEvent(true, key) }, ktfNativeKeyPress},
				{"auto-repeat", func() (bool, error) { return r.QueueKeyRepeat(key) }, ktfNativeKeyRepeat},
				{"release", func() (bool, error) { return r.QueueKeyEvent(false, key) }, ktfNativeKeyRelease},
			} {
				before := len(events)
				for attempt := 0; ; attempt++ {
					queued, err := edge.queue()
					check(t, err)
					if queued {
						break
					}
					if attempt == 100 {
						t.Fatalf("%s never reached the card", edge.name)
					}
					step()
				}
				for range 100 {
					if len(events) > before {
						break
					}
					step()
				}
				if len(events) != before+1 {
					t.Fatalf("%s produced %d native key events, want 1", edge.name, len(events)-before)
				}
				if events[before] != edge.native || int32(keys[before]) != key {
					t.Fatalf("%s reached native code as event %d key %d, want event %d key %d",
						edge.name, events[before], int32(keys[before]), edge.native, key)
				}
			}
		})
	}
}

func findAuthorizedIssue547Package(t *testing.T, digest string) ktfloader.Package {
	t.Helper()
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var pkg ktfloader.Package
	found := errors.New("exact package found")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(payload)
		if hex.EncodeToString(sum[:]) != digest {
			return nil
		}
		pkg, err = ktfloader.Inspect(payload)
		if err != nil {
			return err
		}
		return found
	})
	if err != nil && !errors.Is(err, found) {
		t.Fatal(err)
	}
	if pkg.Client == nil {
		t.Skipf("ARAM_TEST_DATA holds no package with digest %s", digest)
	}
	return pkg
}
