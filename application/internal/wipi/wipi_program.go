package wipi

import (
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"sort"
)

const (
	wipiKernelNoMemory = int32(-3)
	wipiAccessDenied   = int32(-5)
)

func DefaultPrograms() map[int32]*Program {
	return map[int32]*Program{
		1: {
			Id:          1,
			ParentID:    0,
			programType: ProgramTypeCApp,
			accessLevel: DefaultAccessLevel,
			Running:     true,
			ExecName:    "wipi-app",
			programName: "wipi-app",
			version:     "1.0",
			vendor:      "local",
		},
	}
}

func (r *Runtime) RegisterProgram(
	execName string,
	programName string,
	version string,
	vendor string,
	programType int32,
	accessLevel int32,
) int32 {
	if !validWIPIProgramName(execName) ||
		(programName != "" && !validWIPIProgramName(programName)) ||
		(version != "" && !validWIPIProgramName(version)) ||
		(vendor != "" && !validWIPIProgramName(vendor)) {
		return guest.WIPIInvalid
	}
	if programName == "" {
		programName = execName
	}
	if programType <= 0 {
		programType = ProgramTypeCApp
	}
	if existing := r.findProgramByExecName(execName); existing != nil {
		existing.programName = programName
		existing.version = version
		existing.vendor = vendor
		existing.programType = programType
		existing.accessLevel = accessLevel
		return existing.Id
	}
	if len(r.Programs) >= wipiMaxPrograms || r.nextProgram <= 0 {
		return wipiKernelNoMemory
	}
	id := r.nextProgram
	r.nextProgram++
	r.Programs[id] = &Program{
		Id:          id,
		ParentID:    r.appManager,
		programType: programType,
		accessLevel: accessLevel,
		ExecName:    execName,
		programName: programName,
		version:     version,
		vendor:      vendor,
	}
	return id
}

func (r *Runtime) getExecNames(
	programNameAddress uint32,
	versionAddress uint32,
	vendorAddress uint32,
	outputAddress uint32,
	outputSize int32,
) (int32, error) {
	if outputAddress == 0 || outputSize <= 0 {
		return guest.WIPIInvalid, nil
	}
	programName, err := r.readOptionalWIPIString(programNameAddress)
	if err != nil {
		return 0, err
	}
	version, err := r.readOptionalWIPIString(versionAddress)
	if err != nil {
		return 0, err
	}
	vendor, err := r.readOptionalWIPIString(vendorAddress)
	if err != nil {
		return 0, err
	}

	output := make([]byte, 0, min(int(outputSize), wipiMaxPrograms*wipiMaxProgramName))
	seen := make(map[string]bool)
	count := int32(0)
	shortBuffer := false
	for _, id := range sortedWIPIProgramIDs(r.Programs) {
		program := r.Programs[id]
		if program == nil ||
			!matchesWIPIProgram(program, programName, version, vendor) ||
			seen[program.ExecName] {
			continue
		}
		seen[program.ExecName] = true
		count++
		entry := append([]byte(program.ExecName), 0)
		if shortBuffer {
			continue
		}
		remaining := int(outputSize) - len(output)
		if len(entry) <= remaining {
			output = append(output, entry...)
			continue
		}
		shortBuffer = true
		if remaining > 0 {
			output = append(output, entry[:remaining]...)
			output[len(output)-1] = 0
		}
	}
	if len(output) == 0 {
		output = []byte{0}
	}
	if err := r.CPU.WriteMemory(outputAddress, output); err != nil {
		return 0, err
	}
	if shortBuffer {
		return guest.WIPIShortBuffer, nil
	}
	return count, nil
}

func (r *Runtime) executeProgram(execNameAddress uint32, parameterCount int32) (int32, error) {
	if execNameAddress == 0 ||
		parameterCount < 0 ||
		parameterCount > wipiMaxExecuteArguments {
		return guest.WIPIInvalid, nil
	}
	execNameBytes, err := r.ReadCString(execNameAddress)
	if err != nil {
		return 0, err
	}
	execName := string(execNameBytes)
	if !validWIPIProgramName(execName) {
		return guest.WIPIInvalid, nil
	}
	arguments := make([]string, 0, parameterCount)
	for index := int32(0); index < parameterCount; index++ {
		address, err := r.arg(int(index) + 2)
		if err != nil {
			return 0, err
		}
		if address == 0 {
			return guest.WIPIInvalid, nil
		}
		value, err := r.ReadCString(address)
		if err != nil {
			return 0, err
		}
		if len(value) >= wipiMaxProgramName {
			return guest.WIPIInvalid, nil
		}
		arguments = append(arguments, string(value))
	}

	r.LastExecuteName = execName
	r.LastExecuteArgs = append(r.LastExecuteArgs[:0], arguments...)
	r.LastExecuted = 0
	template := r.findProgramByExecName(execName)
	if template == nil {
		return guest.WIPIInvalid, nil
	}
	if len(r.Programs) >= wipiMaxPrograms || r.nextProgram <= 0 {
		return wipiKernelNoMemory, nil
	}
	child := *template
	child.Id = r.nextProgram
	child.ParentID = r.CurrentProgram
	child.Running = true
	r.nextProgram++
	r.Programs[child.Id] = &child
	r.LastExecuted = child.Id
	return child.Id, nil
}

func (r *Runtime) stopProgram(id int32) int32 {
	if id <= 0 {
		return guest.WIPIInvalid
	}
	target := r.Programs[id]
	if target == nil || !target.Running {
		return guest.WIPIInvalid
	}
	if target.Id == r.CurrentProgram ||
		(target.ParentID != r.CurrentProgram && r.CurrentProgram != r.appManager) ||
		target.programType == wipiProgramTypeCDLL ||
		target.programType == wipiProgramTypeJavaDLL ||
		target.programType == wipiProgramTypeJavaSysDLL {
		return wipiAccessDenied
	}
	target.Running = false
	return 0
}

func (r *Runtime) getProgramInfo(outputAddress uint32, outputSize int32) (int32, error) {
	if outputAddress == 0 || outputSize < 0 || uint32(outputSize) > maxWIPICopy {
		return guest.WIPIInvalid, nil
	}
	capacity := int(outputSize / 4)
	count := int32(0)
	maximumID := int32(0)
	for _, program := range r.Programs {
		if program != nil && program.Running {
			count++
			maximumID = max(maximumID, program.Id)
		}
	}
	encoded := make([]byte, capacity*4)
	if int32(capacity) <= maximumID {
		if len(encoded) > 0 {
			if err := r.CPU.WriteMemory(outputAddress, encoded); err != nil {
				return 0, err
			}
		}
		return guest.WIPIShortBuffer, nil
	}
	for _, program := range r.Programs {
		if program != nil && program.Running {
			binary.LittleEndian.PutUint32(encoded[int(program.Id)*4:], uint32(program.programType))
		}
	}
	if len(encoded) > 0 {
		if err := r.CPU.WriteMemory(outputAddress, encoded); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func (r *Runtime) getProgramName(outputAddress uint32, outputSize int32) (int32, error) {
	if outputAddress == 0 || outputSize <= 0 {
		return guest.WIPIInvalid, nil
	}
	name := "wipi-app"
	if program := r.Programs[r.CurrentProgram]; program != nil {
		if program.programName != "" {
			name = program.programName
		} else if program.ExecName != "" {
			name = program.ExecName
		}
	}
	encoded := append([]byte(name), 0)
	if len(encoded) > int(outputSize) {
		encoded = encoded[:outputSize]
		encoded[len(encoded)-1] = 0
		if err := r.CPU.WriteMemory(outputAddress, encoded); err != nil {
			return 0, err
		}
		return guest.WIPIShortBuffer, nil
	}
	if err := r.CPU.WriteMemory(outputAddress, encoded); err != nil {
		return 0, err
	}
	return 0, nil
}

func (r *Runtime) readOptionalWIPIString(address uint32) (*string, error) {
	if address == 0 {
		return nil, nil
	}
	value, err := r.ReadCString(address)
	if err != nil {
		return nil, err
	}
	result := string(value)
	return &result, nil
}

func (r *Runtime) findProgramByExecName(execName string) *Program {
	for _, id := range sortedWIPIProgramIDs(r.Programs) {
		program := r.Programs[id]
		if program != nil && program.ExecName == execName {
			return program
		}
	}
	return nil
}

func matchesWIPIProgram(
	program *Program,
	programName *string,
	version *string,
	vendor *string,
) bool {
	return (programName == nil || program.programName == *programName) &&
		(version == nil || program.version == *version) &&
		(vendor == nil || program.vendor == *vendor)
}

func sortedWIPIProgramIDs(programs map[int32]*Program) []int32 {
	ids := make([]int32, 0, len(programs))
	for id := range programs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func validWIPIProgramName(value string) bool {
	return value != "" && len(value) < wipiMaxProgramName
}
