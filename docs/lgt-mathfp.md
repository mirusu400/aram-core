# LGT MathFP: narrow signed 20.12 support

## Evidence and scope

Source: public [LG MMPP MathFP Javadoc](https://nikita36078.github.io/J2ME_Docs/docs/LG_MMPP_API/mmpp/lang/MathFP.html), consulted 2026-09-11. Implementation and synthetic class fixtures are independently authored. No handset/reference implementation source or private class bytes are included.

`mmpp/lang/MathFP` is installed only under `NativePolicyLGT` (serialized policy 2). It is not installed for SKT or generic J2ME. This addresses the observed missing `parseFP(I)I` API cluster, not full handset compatibility, successful game startup, or a verified original-title progression result.

## Explicit public guarantees implemented

The documentation defines a signed 32-bit word with 20 integer bits (including sign) and 12 fractional bits. One raw unit is 1/4096. The integer conversion input range is inclusive `[-524288, 524287]`.

| API | Implemented behavior |
| --- | --- |
| `parseFP(I)I` | Multiply an in-range integer by 4096. Out-of-range input throws guest `java/lang/NumberFormatException`. |
| `toInt(I)I` | Round the fixed value to an integer, rather than truncate it. |
| `round(I)I` | Round to the nearest integer, returned in fixed-point encoding. |
| `abs(I)I` | Absolute value of the fixed word, subject to the endpoint choice below. |
| `add(II)I`, `sub(II)I` | Fixed-point addition and subtraction. |
| `min(II)I`, `max(II)I` | Signed comparison of the fixed-point words. |
| `multiply(II)I` | Use a signed 64-bit intermediate and rescale by 4096. Out-of-range result throws guest `java/lang/ArithmeticException`. |
| `divide(II)I` | Scale the numerator by 4096 in a signed 64-bit intermediate and divide. Zero denominator throws guest `java/lang/ArithmeticException`. |

The divide prose repeats the multiplication wording, but its `Returns: i/j` and zero-denominator exception identify division. No floating-point calculations are used.

The six public integer fields are installed for `getstatic`:

| Field | Raw value | Basis |
| --- | ---: | --- |
| `E` | 11134 | Nearest integer to e * 4096, as required by the nearest-fixed-value wording. |
| `PI` | 12868 | Nearest integer to pi * 4096. |
| `MAX_VALUE_INT` | 524287 | Documented integer endpoint. |
| `MIN_VALUE_INT` | -524288 | Documented integer endpoint. |
| `MAX_VALUE` | 2147483647 | Largest signed 32-bit raw fixed word. |
| `MIN_VALUE` | -2147483648 | Smallest signed 32-bit raw fixed word. |

## Emulator choices, not verified handset behavior

The public page does not supply rounding tie rules, fractional arithmetic quantization rules, or general overflow rules. These deterministic defaults are deliberately explicit:

* `toInt` and `round` choose nearest, with exact halves toward positive infinity. Thus +0.5 becomes 1, -0.5 becomes 0, and -1.5 becomes -1. The rounding addition is widened before shifting. `toInt(MAX_VALUE)` can consequently return 524288.
* Multiplication and division discard fractional raw units toward zero, including negative results. Multiplication checks the exact scaled product against the raw signed-word range **before** quantization, so a fractional excess cannot be hidden by truncation. The precise pre-quantization endpoint treatment is an emulator choice.
* Addition, subtraction, division overflow, `abs(MIN_VALUE)`, and overflow while re-encoding `round` wrap to the low signed 32 bits. The page specifies no overflow exceptions for these cases. In particular, `round(MAX_VALUE)` and `abs(MIN_VALUE)` produce `MIN_VALUE`. This is a chosen Java-integer-like default, not a claim that the handset behaves this way.
* `MAX_VALUE` means the full raw signed-word maximum, representing 524287 + 4095/4096. The prose describes the integer range imprecisely as ending at 524287 and does not print a numeric `MAX_VALUE` constant. Using all fractional bits at the upper endpoint is the representation-based emulator interpretation, not recovered constant bytes.

## Deliberately unsupported

`parseFP(String)`, `toString(int)`, `pow`, `sqrt`, `log`, `exp`, and all six trigonometric/inverse-trigonometric functions remain unregistered. Unsupported calls fail instead of returning invented zeroes, floating-point approximations, or guessed string conversions. Constructor semantics are not added.

## Verification

`skvm/natives_lgt_math_test.go` contains literal, independently specified table expectations for positive/negative values, conversion endpoints and out-of-range inputs, ties, sub-unit truncation, signed comparisons, wide intermediates, multiplication overflow, division by zero, and chosen wrapping defaults.

Tests first failed through `VM.InvokeStatic` with `SKVM class "mmpp/lang/MathFP" is unavailable`, before the coordinator installed the LGT-only hook. After integration, tests invoke the normal VM construction and dispatch path, assert actual guest throwable types, and verify SKT/J2ME isolation and unsupported methods. Independently generated JVM class bytes exercise `invokestatic`, every constant through `getstatic`, and guest exception handlers for conversion, multiplication overflow, and division by zero. Instruction counters confirm execution rather than direct native-only fixture calls.

Validation commands:

```text
go test ./skvm -run "TestLGTMathFP|Test.*NativePolicy" -count=1
go vet ./skvm
```

These establish synthetic API behavior and integration, not full handset conformance or private-title success.
