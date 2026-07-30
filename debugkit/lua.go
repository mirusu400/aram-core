package debugkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

type luaRunner struct {
	session *Session
	output  *json.Encoder
}

// RunLuaFile executes a trusted local Lua scenario against the session.
func (s *Session) RunLuaFile(
	ctx context.Context,
	path string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Lua script %q: %w", path, err)
	}
	return s.RunLua(ctx, path, string(source), stdout, stderr)
}

// RunLua executes a trusted Lua scenario. Debug actions and print calls are
// written as one JSON object per line to stdout.
func (s *Session) RunLua(
	ctx context.Context,
	name string,
	source string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	// GopherLua's standard I/O library binds to process file descriptors.
	// The debugger replaces print and keeps its own action stream on stdout;
	// stderr remains owned by the caller for the returned execution error.
	_ = stderr
	state := lua.NewState()
	defer state.Close()
	state.SetContext(ctx)

	runner := &luaRunner{
		session: s,
		output:  json.NewEncoder(stdout),
	}
	runner.install(state)
	if err := state.DoString(source); err != nil {
		return fmt.Errorf("execute Lua script %q: %w", name, err)
	}
	return nil
}

func (r *luaRunner) install(state *lua.LState) {
	module := state.NewTable()
	state.SetFuncs(module, map[string]lua.LGFunction{
		"start":         r.start,
		"step":          r.step,
		"key_down":      r.keyDown,
		"key_up":        r.keyUp,
		"tap":           r.tap,
		"reset":         r.reset,
		"stop":          r.stop,
		"status":        r.status,
		"cpu":           r.cpu,
		"runtime":       r.runtime,
		"screen":        r.screen,
		"pixel":         r.pixel,
		"screenshot":    r.screenshot,
		"save_state":    r.saveState,
		"load_state":    r.loadState,
		"assert_screen": r.assertScreen,
		"log":           r.log,
	})
	state.SetGlobal("aram", module)
	state.SetGlobal("print", state.NewFunction(r.log))
}

func (r *luaRunner) start(state *lua.LState) int {
	if err := r.session.Start(state.Context()); err != nil {
		state.RaiseError("start: %v", err)
	}
	return r.pushStatus(state, "start")
}

func (r *luaRunner) step(state *lua.LState) int {
	count := state.OptInt(1, 1)
	if err := r.session.Step(state.Context(), count); err != nil {
		state.RaiseError("step: %v", err)
	}
	return r.pushStatus(state, "step")
}

func (r *luaRunner) keyDown(state *lua.LState) int {
	control := state.CheckString(1)
	if err := r.session.KeyDown(control); err != nil {
		state.RaiseError("key_down: %v", err)
	}
	return r.pushStatus(state, "key_down")
}

func (r *luaRunner) keyUp(state *lua.LState) int {
	control := state.CheckString(1)
	if err := r.session.KeyUp(control); err != nil {
		state.RaiseError("key_up: %v", err)
	}
	return r.pushStatus(state, "key_up")
}

func (r *luaRunner) tap(state *lua.LState) int {
	control := state.CheckString(1)
	holdFrames := state.OptInt(2, 1)
	if err := r.session.Tap(state.Context(), control, holdFrames); err != nil {
		state.RaiseError("tap: %v", err)
	}
	return r.pushStatus(state, "tap")
}

func (r *luaRunner) reset(state *lua.LState) int {
	if err := r.session.Reset(state.Context()); err != nil {
		state.RaiseError("reset: %v", err)
	}
	return r.pushStatus(state, "reset")
}

func (r *luaRunner) stop(state *lua.LState) int {
	if err := r.session.Stop(); err != nil {
		state.RaiseError("stop: %v", err)
	}
	return r.pushStatus(state, "stop")
}

func (r *luaRunner) status(state *lua.LState) int {
	return r.pushStatus(state, "status")
}

func (r *luaRunner) cpu(state *lua.LState) int {
	report, err := r.session.CPU()
	if err != nil {
		state.RaiseError("cpu: %v", err)
	}
	if err := r.emit("cpu", report); err != nil {
		state.RaiseError("cpu: %v", err)
	}
	state.Push(cpuToLua(state, report))
	return 1
}

func (r *luaRunner) runtime(state *lua.LState) int {
	diagnostics, ok := r.session.Diagnostics()
	if !ok {
		state.RaiseError("runtime: machine does not expose runtime diagnostics")
	}
	if err := r.emit("runtime", diagnostics); err != nil {
		state.RaiseError("runtime: %v", err)
	}
	state.Push(anyToLua(state, diagnostics))
	return 1
}

func (r *luaRunner) screen(state *lua.LState) int {
	report := r.session.Screen()
	if err := r.emit("screen", report); err != nil {
		state.RaiseError("screen: %v", err)
	}
	state.Push(screenToLua(state, report))
	return 1
}

func (r *luaRunner) pixel(state *lua.LState) int {
	x, y := state.CheckInt(1), state.CheckInt(2)
	pixel, err := r.session.Pixel(x, y)
	if err != nil {
		state.RaiseError("pixel: %v", err)
	}
	if err := r.emit("pixel", map[string]any{
		"x": x, "y": y, "rgba": pixel,
	}); err != nil {
		state.RaiseError("pixel: %v", err)
	}
	state.Push(pixelToLua(state, pixel))
	return 1
}

func (r *luaRunner) screenshot(state *lua.LState) int {
	path := state.CheckString(1)
	report, err := r.session.Screenshot(path)
	if err != nil {
		state.RaiseError("screenshot: %v", err)
	}
	if err := r.emit("screenshot", map[string]any{
		"path":   path,
		"screen": report,
	}); err != nil {
		state.RaiseError("screenshot: %v", err)
	}
	state.Push(screenToLua(state, report))
	return 1
}

func (r *luaRunner) saveState(state *lua.LState) int {
	path := state.CheckString(1)
	if err := r.session.SaveState(path); err != nil {
		state.RaiseError("save_state: %v", err)
	}
	if err := r.emit("save_state", map[string]string{"path": path}); err != nil {
		state.RaiseError("save_state: %v", err)
	}
	return 0
}

func (r *luaRunner) loadState(state *lua.LState) int {
	path := state.CheckString(1)
	if err := r.session.LoadState(path); err != nil {
		state.RaiseError("load_state: %v", err)
	}
	if err := r.emit("load_state", map[string]string{"path": path}); err != nil {
		state.RaiseError("load_state: %v", err)
	}
	return 0
}

func (r *luaRunner) assertScreen(state *lua.LState) int {
	expected := strings.ToLower(strings.TrimSpace(state.CheckString(1)))
	actual := r.session.Screen().RGBASHA256
	if expected != actual {
		state.RaiseError("screen SHA-256 mismatch: expected %s, got %s", expected, actual)
	}
	if err := r.emit("assert_screen", map[string]string{"rgba_sha256": actual}); err != nil {
		state.RaiseError("assert_screen: %v", err)
	}
	return 0
}

func (r *luaRunner) log(state *lua.LState) int {
	values := make([]string, 0, state.GetTop())
	for index := 1; index <= state.GetTop(); index++ {
		values = append(values, state.Get(index).String())
	}
	if err := r.emit("log", map[string]string{"message": strings.Join(values, "\t")}); err != nil {
		state.RaiseError("log: %v", err)
	}
	return 0
}

func (r *luaRunner) pushStatus(state *lua.LState, event string) int {
	status := r.session.Status()
	if err := r.emit(event, status); err != nil {
		state.RaiseError("%s: %v", event, err)
	}
	state.Push(statusToLua(state, status))
	return 1
}

func (r *luaRunner) emit(event string, result any) error {
	return r.output.Encode(map[string]any{
		"event":  event,
		"result": result,
	})
}

func statusToLua(state *lua.LState, status Status) *lua.LTable {
	table := state.NewTable()
	table.RawSetString("state", lua.LString(status.State))
	table.RawSetString("frame", lua.LNumber(status.Frame))
	table.RawSetString("elapsed_ms", lua.LNumber(status.ElapsedMS))
	table.RawSetString("screen", screenToLua(state, status.Screen))
	if status.CPU != nil {
		table.RawSetString("cpu", cpuToLua(state, *status.CPU))
	}
	if status.Runtime != nil {
		table.RawSetString("runtime", anyToLua(state, status.Runtime))
	}
	return table
}

func anyToLua(state *lua.LState, value any) lua.LValue {
	switch value := value.(type) {
	case nil:
		return lua.LNil
	case string:
		return lua.LString(value)
	case bool:
		return lua.LBool(value)
	case int:
		return lua.LNumber(value)
	case int32:
		return lua.LNumber(value)
	case int64:
		return lua.LNumber(value)
	case uint:
		return lua.LNumber(value)
	case uint8:
		return lua.LNumber(value)
	case uint16:
		return lua.LNumber(value)
	case uint32:
		return lua.LNumber(value)
	case uint64:
		return lua.LNumber(value)
	case float32:
		return lua.LNumber(value)
	case float64:
		return lua.LNumber(value)
	case map[string]any:
		table := state.NewTable()
		for key, element := range value {
			table.RawSetString(key, anyToLua(state, element))
		}
		return table
	case []any:
		table := state.NewTable()
		for index, element := range value {
			table.RawSetInt(index+1, anyToLua(state, element))
		}
		return table
	case []string:
		table := state.NewTable()
		for index, element := range value {
			table.RawSetInt(index+1, lua.LString(element))
		}
		return table
	default:
		return lua.LString(fmt.Sprint(value))
	}
}

func cpuToLua(state *lua.LState, report CPUReport) *lua.LTable {
	table := state.NewTable()
	registers := state.NewTable()
	for name, value := range report.Registers {
		registers.RawSetString(name, lua.LNumber(value))
	}
	table.RawSetString("registers", registers)
	if report.LastResult != nil {
		result := state.NewTable()
		result.RawSetString("reason", lua.LString(report.LastResult.Reason))
		result.RawSetString("reason_code", lua.LNumber(report.LastResult.ReasonCode))
		result.RawSetString("instructions", lua.LNumber(report.LastResult.Instructions))
		result.RawSetString("pc", lua.LNumber(report.LastResult.PC))
		if report.LastResult.Error != "" {
			result.RawSetString("error", lua.LString(report.LastResult.Error))
		}
		table.RawSetString("last_result", result)
	}
	return table
}

func screenToLua(state *lua.LState, report ScreenReport) *lua.LTable {
	table := state.NewTable()
	table.RawSetString("min_x", lua.LNumber(report.MinX))
	table.RawSetString("min_y", lua.LNumber(report.MinY))
	table.RawSetString("width", lua.LNumber(report.Width))
	table.RawSetString("height", lua.LNumber(report.Height))
	table.RawSetString("rgba_sha256", lua.LString(report.RGBASHA256))
	table.RawSetString("non_black_pixels", lua.LNumber(report.NonBlackPixels))
	table.RawSetString("visible_pixels", lua.LNumber(report.VisiblePixels))
	return table
}

func pixelToLua(state *lua.LState, pixel Pixel) *lua.LTable {
	table := state.NewTable()
	table.RawSetString("r", lua.LNumber(pixel.R))
	table.RawSetString("g", lua.LNumber(pixel.G))
	table.RawSetString("b", lua.LNumber(pixel.B))
	table.RawSetString("a", lua.LNumber(pixel.A))
	return table
}
