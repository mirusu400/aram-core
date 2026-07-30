aram.start()
aram.step(1)

local cpu = aram.cpu()
print(
    "cpu",
    "pc=" .. string.format("0x%08x", cpu.registers.pc),
    "cpsr=" .. string.format("0x%08x", cpu.registers.cpsr),
    "stop=" .. cpu.last_result.reason
)

local runtime = aram.runtime()
if runtime.wipi then
    print(
        "wipi",
        "present=" .. runtime.wipi.present_count,
        "calls=" .. runtime.wipi.api_calls,
        "last=" .. runtime.wipi.last_api
    )
end

local screen = aram.screenshot("build/debug/smoke.png")
print(
    "frame",
    screen.width .. "x" .. screen.height,
    screen.rgba_sha256,
    "non_black=" .. screen.non_black_pixels
)

local center = aram.pixel(
    math.floor(screen.width / 2),
    math.floor(screen.height / 2)
)
print("center", center.r, center.g, center.b, center.a)
