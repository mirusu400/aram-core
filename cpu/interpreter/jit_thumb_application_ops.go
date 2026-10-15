package interpreter

// These dense application-only opcodes occupy the byte already reserved for
// full-block specialization. The guarded executor retains the architectural
// class and raw encoding; no operand or instruction-cache data is enlarged.
const (
	thumbAppImmediateTransferBase thumbInstructionClass = thumbHighRegister + thumbDeadFlagsOffset + 1
	thumbAppRegisterTransferBase                        = thumbAppImmediateTransferBase + 4
)

func thumbApplicationOpcode(op thumbInstructionClass, raw uint16) thumbInstructionClass {
	switch op {
	case thumbImmediateTransfer:
		return thumbAppImmediateTransferBase + thumbInstructionClass(raw>>11&3)
	case thumbRegisterTransfer:
		return thumbAppRegisterTransferBase + thumbInstructionClass(raw>>9&7)
	default:
		return op
	}
}
