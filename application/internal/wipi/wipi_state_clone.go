package wipi

import (
	"fmt"
	"strings"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

func cloneResources(source map[string]*Resource) map[string]*Resource {
	result := make(map[string]*Resource, len(source))
	for name, resource := range source {
		if resource == nil {
			continue
		}
		result[name] = &Resource{
			Id:   resource.Id,
			name: resource.name,
			Data: append([]byte(nil), resource.Data...),
		}
	}
	return result
}

func cloneDatabases(source map[string]*Database) map[string]*Database {
	result := make(map[string]*Database, len(source))
	for key, database := range source {
		clone := &Database{
			Name:       database.Name,
			RecordSize: database.RecordSize,
			Mode:       database.Mode,
			NextRecord: database.NextRecord,
			Records:    make(map[int32][]byte, len(database.Records)),
		}
		for recordID, record := range database.Records {
			clone.Records[recordID] = append([]byte(nil), record...)
		}
		result[key] = clone
	}
	return result
}

func cloneComponents(source map[uint32]*Component) map[uint32]*Component {
	result := make(map[uint32]*Component, len(source))
	for handle, component := range source {
		clone := *component
		clone.Label = append([]byte(nil), component.Label...)
		clone.text = append([]byte(nil), component.text...)
		clone.Callbacks = make(map[int32]UICallback, len(component.Callbacks))
		for index, callback := range component.Callbacks {
			clone.Callbacks[index] = callback
		}
		clone.menuItems = cloneUIItems(component.menuItems)
		clone.listItems = cloneUIItems(component.listItems)
		result[handle] = &clone
	}
	return result
}

func cloneMediaClips(source map[uint32]*wipiMediaClip) map[uint32]*wipiMediaClip {
	result := make(map[uint32]*wipiMediaClip, len(source))
	for handle, clip := range source {
		clone := *clip
		clone.mediaType = append([]byte(nil), clip.mediaType...)
		clone.Data = append([]byte(nil), clip.Data...)
		result[handle] = &clone
	}
	return result
}

func cloneSerialPorts(source map[int32]*wipiSerialPort) map[int32]*wipiSerialPort {
	result := make(map[int32]*wipiSerialPort, len(source))
	for descriptor, port := range source {
		clone := *port
		clone.Data = append([]byte(nil), port.Data...)
		result[descriptor] = &clone
	}
	return result
}

func cloneSockets(source map[int32]*wipiSocket) map[int32]*wipiSocket {
	result := make(map[int32]*wipiSocket, len(source))
	for descriptor, socket := range source {
		clone := *socket
		clone.readData = append([]byte(nil), socket.readData...)
		clone.writeData = append([]byte(nil), socket.writeData...)
		result[descriptor] = &clone
	}
	return result
}

func cloneHTTP(source map[int32]*wipiHTTP) map[int32]*wipiHTTP {
	result := make(map[int32]*wipiHTTP, len(source))
	for descriptor, request := range source {
		clone := *request
		clone.url = append([]byte(nil), request.url...)
		clone.method = append([]byte(nil), request.method...)
		clone.request = append([]byte(nil), request.request...)
		clone.response = append([]byte(nil), request.response...)
		clone.properties = guest.CloneSliceMap(request.properties)
		result[descriptor] = &clone
	}
	return result
}

func cloneUIItems(source []wipiUIItem) []wipiUIItem {
	result := make([]wipiUIItem, len(source))
	for index, item := range source {
		result[index] = wipiUIItem{
			Label: append([]byte(nil), item.Label...),
			image: item.image,
		}
	}
	return result
}

func validSavedWIPIPath(name string) bool {
	return name == "/private" || name == "/shared" || name == "/system" ||
		strings.HasPrefix(name, "/private/") ||
		strings.HasPrefix(name, "/shared/") ||
		strings.HasPrefix(name, "/system/")
}

func validateWIPIProgramState(
	programs map[int32]*Program,
	nextProgram int32,
	currentProgram int32,
	appManager int32,
	lastExecuteName string,
	lastExecuteArgs []string,
	lastExecuted int32,
) error {
	if len(programs) == 0 || len(programs) > wipiMaxPrograms {
		return fmt.Errorf("program count %d is invalid", len(programs))
	}
	highestID := int32(0)
	for id, program := range programs {
		if program == nil || id <= 0 || program.Id != id ||
			program.ParentID < 0 || program.programType <= 0 ||
			!validWIPIProgramName(program.ExecName) ||
			!validWIPIProgramName(program.programName) ||
			len(program.version) >= wipiMaxProgramName ||
			len(program.vendor) >= wipiMaxProgramName {
			return fmt.Errorf("program %d has invalid metadata", id)
		}
		highestID = max(highestID, id)
	}
	for id, program := range programs {
		if program.ParentID != 0 && programs[program.ParentID] == nil {
			return fmt.Errorf("program %d has unknown parent %d", id, program.ParentID)
		}
	}
	if nextProgram <= highestID {
		return fmt.Errorf("next identifier %d does not follow %d", nextProgram, highestID)
	}
	if programs[currentProgram] == nil || !programs[currentProgram].Running {
		return fmt.Errorf("current program %d is absent or stopped", currentProgram)
	}
	if programs[appManager] == nil || !programs[appManager].Running {
		return fmt.Errorf("application manager %d is absent or stopped", appManager)
	}
	if lastExecuteName != "" && !validWIPIProgramName(lastExecuteName) {
		return fmt.Errorf("last execute name is invalid")
	}
	if len(lastExecuteArgs) > wipiMaxExecuteArguments {
		return fmt.Errorf("last execute argument count exceeds limit")
	}
	for _, argument := range lastExecuteArgs {
		if len(argument) >= wipiMaxProgramName {
			return fmt.Errorf("last execute argument exceeds limit")
		}
	}
	if lastExecuted != 0 && programs[lastExecuted] == nil {
		return fmt.Errorf("last executed program %d is absent", lastExecuted)
	}
	return nil
}

func ClonePrograms(source map[int32]*Program) map[int32]*Program {
	result := make(map[int32]*Program, len(source))
	for id, program := range source {
		if program == nil {
			continue
		}
		clone := *program
		result[id] = &clone
	}
	return result
}
