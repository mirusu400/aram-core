# Raptor callback execution allowance

`Factory.RaptorFrameRunBudget` optionally supplies an instruction allowance for
Raptor Clet callbacks. Zero preserves `FrameRunBudget` and its `RunBudget`
fallback, including small deterministic debugger slices. The product adapter
selects `DefaultRaptorFrameRunBudget` (24 million instructions). This is a
bounded execution allowance, not a claim about a handset CPU clock.

Callbacks still stop when they complete, present, yield, or exhaust the budget.
Virtual time advances once per video quantum, and timers keep their existing
deadlines. Other application formats and Raptor Java thread quanta are unchanged.

Issue #322's exact archive SHA-256 is
`c1718bcdf8a904a27a6f252dac6b9cc2801e8303639126a944bf9785d23764ce`.
At its chapter-introduction checkpoint, 1,500 quanta under the Windows amd64
`fastest` backend produced:

| Instructions per quantum | Presentations | Host elapsed time, approximately |
| --- | ---: | ---: |
| 750,000 | 68 | 1.24 s |
| 6,000,000 | 300 | 5.89 s |
| 12,000,000 | 375 | 7.59 s |
| 24,000,000 | 500 | 9.42 s |

These are single diagnostic measurements, not a cross-device performance
guarantee. The old allowance spread each software-rendered callback across too
many video ticks despite available host CPU capacity. With the new allowance,
a 16-quantum OK press followed by 1,500 quanta passed the reported chapter
screen and reached the opening dialogue without a fault. Synthetic tests check
the explicit allowance, zero-value inheritance, and callback resumption.
