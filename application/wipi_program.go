package application

import (
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	wipiKernelNoMemory = int32(-3)
	wipiAccessDenied   = int32(-5)
)

func defaultWIPIPrograms() map[int32]*wipiProgram {
	return map[int32]*wipiProgram{
		1: {
			id:          1,
			parentID:    0,
			programType: wipiProgramTypeCApp,
			accessLevel: wipiDefaultAccessLevel,
			running:     true,
			execName:    "wipi-app",
			programName: "wipi-app",
			version:     "1.0",
			vendor:      "local",
		},
	}
}

func (r *wipiRuntime) registerProgram(
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
		return wipiInvalid
	}
	if programName == "" {
		programName = execName
	}
	if programType <= 0 {
		programType = wipiProgramTypeCApp
	}
	if existing := r.findProgramByExecName(execName); existing != nil {
		existing.programName = programName
		existing.version = version
		existing.vendor = vendor
		existing.programType = programType
		existing.accessLevel = accessLevel
		return existing.id
	}
	if len(r.programs) >= wipiMaxPrograms || r.nextProgram <= 0 {
		return wipiKernelNoMemory
	}
	id := r.nextProgram
	r.nextProgram++
	r.programs[id] = &wipiProgram{
		id:          id,
		parentID:    r.appManager,
		programType: programType,
		accessLevel: accessLevel,
		execName:    execName,
		programName: programName,
		version:     version,
		vendor:      vendor,
	}
	return id
}

func (r *wipiRuntime) getExecNames(
	programNameAddress uint32,
	versionAddress uint32,
	vendorAddress uint32,
	outputAddress uint32,
	outputSize int32,
) (int32, error) {
	if outputAddress == 0 || outputSize <= 0 {
		return wipiInvalid, nil
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
	for _, id := range sortedWIPIProgramIDs(r.programs) {
		program := r.programs[id]
		if program == nil ||
			!matchesWIPIProgram(program, programName, version, vendor) ||
			seen[program.execName] {
			continue
		}
		seen[program.execName] = true
		count++
		entry := append([]byte(program.execName), 0)
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
	if err := r.cpu.WriteMemory(outputAddress, output); err != nil {
		return 0, err
	}
	if shortBuffer {
		return wipiShortBuffer, nil
	}
	return count, nil
}

func (r *wipiRuntime) executeProgram(execNameAddress uint32, parameterCount int32) (int32, error) {
	if execNameAddress == 0 ||
		parameterCount < 0 ||
		parameterCount > wipiMaxExecuteArguments {
		return wipiInvalid, nil
	}
	execNameBytes, err := r.readCString(execNameAddress)
	if err != nil {
		return 0, err
	}
	execName := string(execNameBytes)
	if !validWIPIProgramName(execName) {
		return wipiInvalid, nil
	}
	arguments := make([]string, 0, parameterCount)
	for index := int32(0); index < parameterCount; index++ {
		address, err := r.arg(int(index) + 2)
		if err != nil {
			return 0, err
		}
		if address == 0 {
			return wipiInvalid, nil
		}
		value, err := r.readCString(address)
		if err != nil {
			return 0, err
		}
		if len(value) >= wipiMaxProgramName {
			return wipiInvalid, nil
		}
		arguments = append(arguments, string(value))
	}

	r.lastExecuteName = execName
	r.lastExecuteArgs = append(r.lastExecuteArgs[:0], arguments...)
	r.lastExecuted = 0
	template := r.findProgramByExecName(execName)
	if template == nil {
		return wipiInvalid, nil
	}
	if len(r.programs) >= wipiMaxPrograms || r.nextProgram <= 0 {
		return wipiKernelNoMemory, nil
	}
	child := *template
	child.id = r.nextProgram
	child.parentID = r.currentProgram
	child.running = true
	r.nextProgram++
	r.programs[child.id] = &child
	r.lastExecuted = child.id
	return child.id, nil
}

func (r *wipiRuntime) stopProgram(id int32) int32 {
	if id <= 0 {
		return wipiInvalid
	}
	target := r.programs[id]
	if target == nil || !target.running {
		return wipiInvalid
	}
	if target.id == r.currentProgram ||
		(target.parentID != r.currentProgram && r.currentProgram != r.appManager) ||
		target.programType == wipiProgramTypeCDLL ||
		target.programType == wipiProgramTypeJavaDLL ||
		target.programType == wipiProgramTypeJavaSysDLL {
		return wipiAccessDenied
	}
	target.running = false
	return 0
}

func (r *wipiRuntime) getProgramInfo(outputAddress uint32, outputSize int32) (int32, error) {
	if outputAddress == 0 || outputSize < 0 || uint32(outputSize) > maxWIPICopy {
		return wipiInvalid, nil
	}
	capacity := int(outputSize / 4)
	count := int32(0)
	maximumID := int32(0)
	for _, program := range r.programs {
		if program != nil && program.running {
			count++
			maximumID = max(maximumID, program.id)
		}
	}
	encoded := make([]byte, capacity*4)
	if int32(capacity) <= maximumID {
		if len(encoded) > 0 {
			if err := r.cpu.WriteMemory(outputAddress, encoded); err != nil {
				return 0, err
			}
		}
		return wipiShortBuffer, nil
	}
	for _, program := range r.programs {
		if program != nil && program.running {
			binary.LittleEndian.PutUint32(encoded[int(program.id)*4:], uint32(program.programType))
		}
	}
	if len(encoded) > 0 {
		if err := r.cpu.WriteMemory(outputAddress, encoded); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func (r *wipiRuntime) getProgramName(outputAddress uint32, outputSize int32) (int32, error) {
	if outputAddress == 0 || outputSize <= 0 {
		return wipiInvalid, nil
	}
	name := "wipi-app"
	if program := r.programs[r.currentProgram]; program != nil {
		if program.programName != "" {
			name = program.programName
		} else if program.execName != "" {
			name = program.execName
		}
	}
	encoded := append([]byte(name), 0)
	if len(encoded) > int(outputSize) {
		encoded = encoded[:outputSize]
		encoded[len(encoded)-1] = 0
		if err := r.cpu.WriteMemory(outputAddress, encoded); err != nil {
			return 0, err
		}
		return wipiShortBuffer, nil
	}
	if err := r.cpu.WriteMemory(outputAddress, encoded); err != nil {
		return 0, err
	}
	return 0, nil
}

func (r *wipiRuntime) readOptionalWIPIString(address uint32) (*string, error) {
	if address == 0 {
		return nil, nil
	}
	value, err := r.readCString(address)
	if err != nil {
		return nil, err
	}
	result := string(value)
	return &result, nil
}

func (r *wipiRuntime) findProgramByExecName(execName string) *wipiProgram {
	for _, id := range sortedWIPIProgramIDs(r.programs) {
		program := r.programs[id]
		if program != nil && program.execName == execName {
			return program
		}
	}
	return nil
}

func matchesWIPIProgram(
	program *wipiProgram,
	programName *string,
	version *string,
	vendor *string,
) bool {
	return (programName == nil || program.programName == *programName) &&
		(version == nil || program.version == *version) &&
		(vendor == nil || program.vendor == *vendor)
}

func sortedWIPIProgramIDs(programs map[int32]*wipiProgram) []int32 {
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

func validateWIPIProgramState(
	programs map[int32]*wipiProgram,
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
		if program == nil || id <= 0 || program.id != id ||
			program.parentID < 0 || program.programType <= 0 ||
			!validWIPIProgramName(program.execName) ||
			!validWIPIProgramName(program.programName) ||
			len(program.version) >= wipiMaxProgramName ||
			len(program.vendor) >= wipiMaxProgramName {
			return fmt.Errorf("program %d has invalid metadata", id)
		}
		highestID = max(highestID, id)
	}
	for id, program := range programs {
		if program.parentID != 0 && programs[program.parentID] == nil {
			return fmt.Errorf("program %d has unknown parent %d", id, program.parentID)
		}
	}
	if nextProgram <= highestID {
		return fmt.Errorf("next identifier %d does not follow %d", nextProgram, highestID)
	}
	if programs[currentProgram] == nil || !programs[currentProgram].running {
		return fmt.Errorf("current program %d is absent or stopped", currentProgram)
	}
	if programs[appManager] == nil || !programs[appManager].running {
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

func clonePrograms(source map[int32]*wipiProgram) map[int32]*wipiProgram {
	result := make(map[int32]*wipiProgram, len(source))
	for id, program := range source {
		if program == nil {
			continue
		}
		clone := *program
		result[id] = &clone
	}
	return result
}
