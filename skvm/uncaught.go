package skvm

import "fmt"

// UncaughtException describes one exception a title threw and nothing in the
// title caught, which the VM absorbed instead of ending the session.
//
// The MIDP reference implementation's event loop logs an exception escaping
// paint, keyPressed or a callSerially Runnable and moves on to the next
// event, and Java ends only the thread an exception escapes Thread.run in.
// Titles shipped with such bugs on paths players use every day - LGT
// 팔콤영웅전설 EP1 walks a two-entry MediaPlayer[] three times whenever its
// volume option changes - which a handset that ended the application there
// would not have let through, so ending the whole session is ARAM's failure,
// not the title's.
type UncaughtException struct {
	// Class is the thrown object's class, e.g. java/lang/NullPointerException.
	Class string
	// Message is the detail ARAM attached when it raised the exception
	// itself; it is empty for an object the title constructed and threw.
	Message string
	// Callback names the host entry point the exception escaped from:
	// "keyPressed", "paint", "callSerially", "Thread.run", and so on.
	Callback string
	// Method and PC locate the innermost frame the exception left without a
	// handler, as "Class.method(descriptor)" and a bytecode offset. Method is
	// empty when ARAM raised it before any guest frame ran.
	Method string
	PC     int
	// Reference is the thrown object's VM reference.
	Reference uint32
	// Text is the fault the VM used to end the session with, so a report
	// that keys on it keeps matching the same defect.
	Text string
}

// maxUncaughtRecords bounds how many absorbed exceptions are kept. A title
// that throws from paint every frame must not grow the log without limit; the
// first ones are the diagnostic, and Count keeps the total.
const maxUncaughtRecords = 16

type uncaughtLog struct {
	count   uint64
	records []UncaughtException
	last    UncaughtException
}

func (e *thrown) noteEscape(current *frame, pc int) {
	if e.escapedFrom != nil || current == nil || current.class == nil {
		return
	}
	e.escapedFrom = &frameSite{
		class:      current.class.Name,
		method:     current.method.Name,
		descriptor: current.method.Descriptor,
		pc:         pc,
	}
}

// absorbUncaught ends a guest exception at an event-loop boundary: it is
// recorded and the caller carries on as if the callback had returned. Every
// other error - a host fault, a yield, a halt - passes through unchanged.
//
// Only top-level dispatch may call this. A callback the title itself invoked
// (a RecordComparator, a Layer's paint, Thread.run called directly) must keep
// propagating so the title's own catch blocks still see it.
func (vm *VM) absorbUncaught(callback string, err error) error {
	exception, ok := err.(*thrown)
	if !ok {
		return err
	}
	record := UncaughtException{
		Class:     exception.class,
		Message:   exception.message,
		Callback:  callback,
		Reference: exception.reference,
		Text:      exception.Error(),
	}
	if site := exception.escapedFrom; site != nil {
		record.Method = fmt.Sprintf("%s.%s%s", site.class, site.method, site.descriptor)
		record.PC = site.pc
	}
	vm.uncaught.count++
	vm.uncaught.last = record
	if len(vm.uncaught.records) < maxUncaughtRecords {
		vm.uncaught.records = append(vm.uncaught.records, record)
	}
	return nil
}

// UncaughtExceptions reports how many exceptions the title let escape an
// event callback or a thread since the VM started, the most recent one, and
// the first few in order. The title kept running past every one of them.
func (vm *VM) UncaughtExceptions() (count uint64, last UncaughtException, first []UncaughtException) {
	first = append([]UncaughtException(nil), vm.uncaught.records...)
	return vm.uncaught.count, vm.uncaught.last, first
}
