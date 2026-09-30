# Multi-game performance follow-up (2026-09-28)

Status: diagnostic only. No optimization from this follow-up was accepted, and the product core pin remains `b4fb941`. Core `d9ef544` was the comparison source. The tested candidates were reverted from source after their measurements.

Main handoff note (2026-09-29): the separate `117db26` and `7470c29` Thumb JIT dispatch changes are the only new core optimization included alongside this report. Exact-hash Node WASM replays measured lower portable-JIT wall times for Itarus, RhythmStar1, and NOM3, with matching output and guest work. Windows default-native, Android x86_64 emulator, and product-paced Chrome results were mixed or close to measured A/A noise, so this is not a universal-speed or release-readiness claim. The later identical-binary Node control varied by -18.28% to +36.49% across five pairs. A clean no-private-input workspace gate passed all eight synthetic cases, all 75 suite unit tests, the core/frontend/integration Go tests and vet, and the SDK examples; Android/arm64 pure-Go core build also passed. The separate authorized 615-input gate remains nonzero because of private-reference failures and three synthetic timeouts under its 20-second, four-job load; those synthetic cases passed in the isolated 60-second, one-job rerun. The product core pin is not advanced. The rejected experiments below remain absent from source; the exact per-game/platform matrix and gate limitations are recorded in `aram-test/benchmarks/jit-unified-loop-dispatch-20260928.md`.

The scenarios are exact-hash, fixed-frame replays. A/B runs were serial and alternating; the helpers checked build-artifact hashes, completed frames, checkpoints, audio identity, and guest advancement. Core-unpaced process CPU time includes startup and warmup, so it is a secondary signal, not game FPS. Browser wall time includes product pacing and browser work. The host had substantial variable background load. Do not infer statistical significance or whole-game playability from the small paired sets below.

| Comparison (candidate relative to baseline) | Host / scenario | Pairs | Paired CPU median | Range | Decision |
|---|---|---:|---:|---:|---|
| `d9ef544` vs product pin | Node / LGT BladeMaster3 input replay | 5 | +11.87% | -2.55% to +19.40% | Possible regression; not a pin-promotion case |
| `d9ef544` vs product pin | Node / LGT SuperActionHero input replay | 5 | 0.00% | -4.73% to +11.08% | No common LGT regression proven |
| Same `d9ef544` binary vs itself (A/A control) | Node / LGT BladeMaster3 | 7 | -3.31% | -8.52% to +2.56% | Quantifies measurement noise |
| Shared counted/specialized-loop slot vs `d9ef544` | Node / LGT BladeMaster3 | 5 | +0.49% | -4.07% to +4.78% | Rejected |
| Shared counted/specialized-loop slot vs `d9ef544` | Node / RhythmStar1, first set | 3 | -11.45% | -12.91% to -11.37% | Not reproduced |
| Shared counted/specialized-loop slot vs `d9ef544` | Node / RhythmStar1, repeat | 3 | +13.39% | -9.65% to +27.89% | Rejected |
| Shared counted/specialized-loop slot vs `d9ef544` | Windows native / RhythmStar1 | 7 | +1.32% | -8.82% to +36.47% | Rejected |
| Cached Thumb stack-read fast path vs `d9ef544` | Windows native / RhythmStar1 | 5 | -3.58% | -10.43% to +2.23% | Insufficient on its own |
| Cached Thumb stack-read fast path vs `d9ef544` | Node / RhythmStar1, repeat | 5 | -1.96% | -5.06% to +13.23% | Mixed with earlier 3-pair regression |
| Cached Thumb stack-read fast path vs `d9ef544` | Node / Itarus | 3 | -4.28% | -13.41% to -2.74% | No detected loss |
| Cached Thumb stack-read fast path vs `d9ef544` | Node / LGT SuperActionHero, repeat | 5 | +8.94% | -7.26% to +13.97% | Rejected: 4/5 slower |
| Cached Thumb stack-read fast path vs `d9ef544` | Windows native / LGT SuperActionHero | 5 | +5.00% | -3.17% to +8.77% | Rejected: 4/5 slower |
| Full-block Thumb slice avoidance vs `d9ef544` | Windows native / RhythmStar1 | 5 | -2.31% | Mixed | Rejected: cross-platform loss |
| Full-block Thumb slice avoidance vs `d9ef544` | Node / RhythmStar1 | 5 | +2.94% | Mixed | Rejected: 4/5 slower |

The stack-read microbenchmark favored the fast path on both Windows native (approximately 3.0 to 2.5 ns/op) and Node WASM (approximately 11.6 to 8.5 ns/op), with zero allocations in both variants. That isolated win did not generalize to the SuperActionHero replay. Do not reintroduce it based only on the microbenchmark.

The earlier headless Chrome startup failure (`0x80000003`) was specific to the prior restricted environment. With the permitted local Chrome, product-paced comparisons completed without an inspector or security-bypass flags. Latest `d9ef544` relative to product pin `b4fb941`:

| Scenario | Environment | Pairs | Paired wall-time median | Range | Faster pairs |
|---|---|---:|---:|---:|---:|
| Itarus | Chrome product-paced JIT | 5 | -4.07% | -6.00% to -1.77% | 5/5 |
| RhythmStar1 | Chrome product-paced JIT | 5 | +2.31% | -3.12% to +5.25% | 2/5 |
| BladeMaster3 | Chrome product-paced JIT | 5 | +0.36% | -3.62% to +5.09% | 2/5 |
| SuperActionHero | Chrome product-paced JIT | 5 | -0.35% | -2.87% to +4.18% | 3/5 |

Android SDK x86_64 API 33 AVD `AramRgbaOpaque20260928` was launched headlessly for core-only JIT comparisons, then stopped. This is **not** a physical ARM phone, Android app/UI, or audio-device measurement.

| Scenario | Environment | Pairs | Paired unpaced wall-time median | Range | Faster pairs |
|---|---|---:|---:|---:|---:|
| Itarus | Android x86_64 emulator JIT | 5 | -21.10% | -25.48% to -16.66% | 5/5 |
| NOM3 | Android x86_64 emulator JIT | 5 | -0.43% | -15.48% to +19.36% | 3/5 |
| RhythmStar1 | Android x86_64 emulator JIT | 5 | +1.63% | -15.70% to +4.65% | 2/5 |
| BladeMaster3 | Android x86_64 emulator JIT | 5 | +6.51% | -7.04% to +23.41% | 1/5 |
| SuperActionHero | Android x86_64 emulator JIT | 5 | +7.22% | -10.63% to +14.70% | 1/5 |

All these completed comparisons had zero output/configuration identity regressions. The Android LGT slowdown was not reproduced in Chrome product-paced SuperActionHero or BladeMaster3; it remains environment-specific and noisy. The new Itarus paths clearly help that title, but the mixed RhythmStar and Android LGT results do not justify promoting the product pin on a universal-speed claim.

The product-pin baseline's Node core-unpaced guest/wall median was about 0.78 for Itarus and RhythmStar1; these remain the highest-impact compute-capacity scenes. LGT BladeMaster3 was approximately 5.85 in that baseline, so a percentage change there is less urgent for realtime capacity. The existing native RhythmStar1 CPU profile attributes about 69% cumulative samples to `executeThumbMicroBlock`; measured stack loads were a visible subpath. Profile samples and the scene ratios are workload-specific, not a universal game ranking.

Retained ignored build evidence is under `build/perf-core-main-lgt-20260928/`, `build/perf-core-main-ktf-chrome-20260928/`, `build/perf-core-main-lgt-sah-chrome-20260928/`, `build/perf-android-core-main-20260928/`, `build/perf-android-core-main-lgt-20260928/`, `build/perf-jit-shared-loop-slot-20260928/`, `build/perf-thumb-stack-read-candidate-20260928/`, and `build/perf-thumb-full-block-slice-candidate-20260928/`. The immutable product-pin comparison builds remain in ignored `build/` trees in this and the sibling test repository. No source candidate is active after this note.
