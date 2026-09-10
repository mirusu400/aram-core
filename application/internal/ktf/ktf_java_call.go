package ktf

import (
	"fmt"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	ktfJavaCallEvent         = uint32(7)
	ktfJavaCallIncomingEvent = uint32(0)
	ktfJavaCallNotifyEvent   = uint32(1)
)

func (r *Runtime) handleCallMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "place(Ljava/lang/String;)V", "place0(Ljava/lang/String;)V":
		numberObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		number := r.javaStringValue(numberObject)
		if !r.javaCall.canStart() {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		sequence, err := r.Services.Device.Request(
			r.ServiceOwner,
			shared.RequestPhone,
			number,
			nil,
			r.Services.Clock.Monotonic(),
		)
		if err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.javaCall = ktfCall{
			state: ktfCallCalling, number: number,
			requestSequence: sequence, ppp: r.javaCall.ppp,
		}
		r.tracef("java_call_place:sequence=%d:number=%s", sequence, number)
		return 0, nil
	case "accept()V", "accept0()V":
		if r.javaCall.state != ktfCallIncoming {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.javaCall.state = ktfCallConnected
		r.trace("java_call_accept")
		return 0, nil
	case "reject()V", "reject0()V":
		if r.javaCall.state != ktfCallIncoming {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.javaCall.state = ktfCallRejected
		r.enqueueJavaEvent(ktfJavaEvent{
			ktfJavaCallEvent, ktfJavaCallNotifyEvent, 0, 0,
		})
		r.trace("java_call_reject")
		return 0, nil
	case "end()V", "end0()V":
		if r.javaCall.state != ktfCallCalling &&
			r.javaCall.state != ktfCallConnected &&
			r.javaCall.state != ktfCallWaiting &&
			r.javaCall.state != ktfCallTransferred {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.javaCall.state = ktfCallEnded
		r.enqueueJavaEvent(ktfJavaEvent{
			ktfJavaCallEvent, ktfJavaCallNotifyEvent, 0, 0,
		})
		r.trace("java_call_end")
		return 0, nil
	case "securePPPSession()V", "securePPPSession0()V":
		_, _, available := r.Services.Device.Status()
		if !available {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.javaCall.ppp = true
		r.trace("java_call_ppp_secured")
		return 0, nil
	default:
		return 0, nil
	}
}

func (call ktfCall) canStart() bool {
	return call.state == ktfCallIdle || call.state == ktfCallRejected ||
		call.state == ktfCallEnded
}

// QueueIncomingCall exposes the handset's incoming-call edge to the KTF
// adapter. It changes the Call state before publishing EventQueue.CALL_EVENT,
// so a listener can immediately accept or reject the call while handling it.
func (r *Runtime) QueueIncomingCall(number string) (bool, error) {
	if !r.javaCall.canStart() || strings.TrimSpace(number) == "" ||
		len(number) > 64 || strings.IndexByte(number, 0) >= 0 {
		return false, fmt.Errorf("invalid or busy incoming call")
	}
	numberObject, err := r.NewJavaString(number)
	if err != nil {
		return false, err
	}
	event := ktfJavaEvent{
		ktfJavaCallEvent, ktfJavaCallIncomingEvent, numberObject, 0,
	}
	if !r.enqueueJavaEvent(event) {
		return false, nil
	}
	r.javaCall = ktfCall{
		state: ktfCallIncoming, number: number, ppp: r.javaCall.ppp,
	}
	r.trace("java_call_incoming:" + number)
	return true, nil
}
