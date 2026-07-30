aram.start()
aram.step(128)

local before = aram.runtime()
local before_screen = aram.screenshot("build/debug/ktf-before-input.png")

aram.tap("ok", 64)
aram.step(4)

local after = aram.runtime()
local after_screen = aram.screenshot("build/debug/ktf-after-input.png")

assert(after.wipi.api_calls > before.wipi.api_calls)
assert(after.wipi.present_count >= before.wipi.present_count)

print(
    "input",
    "calls=" .. before.wipi.api_calls .. "->" .. after.wipi.api_calls,
    "present=" .. before.wipi.present_count .. "->" .. after.wipi.present_count,
    "screen=" .. before_screen.rgba_sha256 .. "->" .. after_screen.rgba_sha256
)
