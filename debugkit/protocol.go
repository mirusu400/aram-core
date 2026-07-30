package debugkit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const MaxProtocolLineBytes = 1 << 20

type ProtocolCommand struct {
	ID         json.RawMessage `json:"id,omitempty"`
	Command    string          `json:"command"`
	Count      *int            `json:"count,omitempty"`
	Control    string          `json:"control,omitempty"`
	HoldFrames *int            `json:"hold_frames,omitempty"`
	Path       string          `json:"path,omitempty"`
	X          int             `json:"x,omitempty"`
	Y          int             `json:"y,omitempty"`
}

type ProtocolResponse struct {
	ID     json.RawMessage `json:"id,omitempty"`
	OK     bool            `json:"ok"`
	Result any             `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ServeProtocol reads one JSON command per line and writes one JSON response
// per line. Command and JSON errors are reported in-band and do not terminate
// the session. An oversized input line terminates the session.
func (s *Session) ServeProtocol(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
) error {
	if input == nil {
		return fmt.Errorf("protocol input is nil")
	}
	if output == nil {
		return fmt.Errorf("protocol output is nil")
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), MaxProtocolLineBytes)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		// Windows PowerShell may prefix redirected text with a UTF-8 BOM.
		line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
		if len(line) == 0 {
			continue
		}
		var command ProtocolCommand
		if err := json.Unmarshal(line, &command); err != nil {
			if encodeErr := encoder.Encode(ProtocolResponse{
				OK:    false,
				Error: fmt.Sprintf("decode command: %v", err),
			}); encodeErr != nil {
				return fmt.Errorf("write protocol response: %w", encodeErr)
			}
			continue
		}
		result, quit, err := s.executeCommand(ctx, command)
		response := ProtocolResponse{
			ID:     command.ID,
			OK:     err == nil,
			Result: result,
		}
		if err != nil {
			response.Error = err.Error()
			response.Result = nil
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("write protocol response: %w", err)
		}
		if quit {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read protocol command: %w", err)
	}
	return nil
}

func (s *Session) executeCommand(
	ctx context.Context,
	command ProtocolCommand,
) (result any, quit bool, err error) {
	switch strings.ToLower(strings.TrimSpace(command.Command)) {
	case "start":
		err = s.Start(ctx)
		if err == nil {
			result = s.Status()
		}
	case "step":
		count := 1
		if command.Count != nil {
			count = *command.Count
		}
		err = s.Step(ctx, count)
		if err == nil {
			result = s.Status()
		}
	case "key_down":
		err = s.KeyDown(command.Control)
		if err == nil {
			result = s.Status()
		}
	case "key_up":
		err = s.KeyUp(command.Control)
		if err == nil {
			result = s.Status()
		}
	case "tap":
		holdFrames := 1
		if command.HoldFrames != nil {
			holdFrames = *command.HoldFrames
		}
		err = s.Tap(ctx, command.Control, holdFrames)
		if err == nil {
			result = s.Status()
		}
	case "reset":
		err = s.Reset(ctx)
		if err == nil {
			result = s.Status()
		}
	case "stop":
		err = s.Stop()
		if err == nil {
			result = s.Status()
		}
	case "status":
		result = s.Status()
	case "cpu":
		result, err = s.CPU()
	case "runtime":
		var ok bool
		result, ok = s.Diagnostics()
		if !ok {
			result = nil
			err = fmt.Errorf("machine does not expose runtime diagnostics")
		}
	case "screen":
		result = s.Screen()
	case "pixel":
		result, err = s.Pixel(command.X, command.Y)
	case "screenshot":
		var report ScreenReport
		report, err = s.Screenshot(command.Path)
		if err == nil {
			result = map[string]any{
				"path":   command.Path,
				"screen": report,
			}
		}
	case "save_state":
		err = s.SaveState(command.Path)
		if err == nil {
			result = map[string]string{"path": command.Path}
		}
	case "load_state":
		err = s.LoadState(command.Path)
		if err == nil {
			result = map[string]string{"path": command.Path}
		}
	case "quit":
		result = map[string]bool{"closed": true}
		quit = true
	case "":
		err = fmt.Errorf("command is empty")
	default:
		err = fmt.Errorf("unknown command %q", command.Command)
	}
	return result, quit, err
}
