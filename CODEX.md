# Codex project guide

## Scope

`aram-core` is the headless emulation library. It must never import a windowing,
GUI, native-dialog, Android Activity, or iOS UIKit package.

The sibling repositories are:

- `aram-frontend`: cross-platform presentation, input, and product shell;
- `aram-emu`: ecosystem roadmap, integration, packaging, and release policy;
- `aram-test`: black-box corpus orchestration, compatibility deltas, and
  failure triage;
- `anycall_magichole`: reverse-engineering evidence and executable reference.

WIPI API documentation reference:

- `llms.txt`: https://mirusu400.github.io/wipi-wiki/llms.txt

Shared runtime architecture:

- `docs/shared-runtime-services.md`: service and adapter boundaries for KTF,
  LGT/LGTP WIPI-C, Raptor, and SKVM.

## Portability contract

- The default build is pure Go.
- Windows, Linux, macOS, and Android/arm64 builds are required.
- CPU implementations are replaceable through `cpu.Backend`.
- Unicorn or another C backend must be optional and isolated behind build tags.
- A portable interpreter backend remains the fallback for platforms where JIT
  or native libraries are unavailable.
- Core tests do not require a display, audio device, Android SDK, or network.
- Guest and save-state formats use fixed-width types and explicit byte order.

## Architecture rules

- `core` owns machine lifecycle and deterministic state contracts.
- `cpu` owns backend-neutral execution contracts.
- `loader` treats every byte as untrusted and returns offset-bearing errors.
- `profile` separates WIPI standard, carrier, manufacturer, device, and title
  behavior.
- Frontends issue commands through exported contracts; they never mutate guest
  memory directly.
- Virtual time, seeded randomness, storage, input, and network responses are
  serializable machine state.
- Game-specific patches are hash-keyed data with expected-original checks.
- Avoid per-instruction callbacks outside debugger or trace modes.

## Evidence and data

- Do not commit firmware, games, memory dumps, device fonts, IDA databases, or
  extracted proprietary assets.
- Use synthetic test fixtures.
- Private integration tests may use `ARAM_REFERENCE_REPO` or `ARAM_TEST_DATA`.
- A title-specific success is not a carrier-wide or WIPI-wide compatibility
  claim.

## Verification

```powershell
gofmt -w .
go test ./...
go vet ./...
$env:GOOS="android"
$env:GOARCH="arm64"
go build ./...
```

Native CI covers Windows, Linux, and macOS. Android is compile-checked because
core code must not depend on desktop services.

## Automatic Git handoff

Finishing a successful file-changing task includes committing and pushing the
task's changes. Do this automatically without waiting for a separate request.

1. Run the verification appropriate to the files and risk of the task.
2. Review `git status` and the final diff before staging.
3. Stage only paths or hunks changed for the current task. Use explicit
   pathspecs; never use `git add -A`, `git add .`, or otherwise sweep unrelated
   work into the commit.
4. Create a concise conventional commit that describes the completed task.
5. Push the new commit to the current branch's configured upstream. If the
   branch has no upstream and `origin` is the intended repository, establish it
   with `git push -u origin HEAD`.

Existing user changes are never part of the automatic handoff unless they were
explicitly placed in the current task. Do not commit generated artifacts,
secrets, proprietary evidence, or private test data. If a file mixes task
changes with unrelated user edits, stage only the task hunks when that can be
done safely; otherwise leave it uncommitted and report the conflict.

Do not commit or push failed, incomplete, or unverified work as if it were
finished. Never amend, rebase, force-push, or bypass hooks unless the user
explicitly requests it. An explicit user request not to commit or push takes
precedence. For work spanning multiple repositories, apply this policy
separately in each repository and report each pushed branch and commit hash.
