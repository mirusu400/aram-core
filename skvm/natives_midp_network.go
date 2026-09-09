package skvm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	commBaudField          = "\x00aram-comm-baud"
	securityCertificate    = "\x00aram-security-certificate"
	certificateSubject     = "\x00aram-certificate-subject"
	certificateIssuer      = "\x00aram-certificate-issuer"
	certificateType        = "\x00aram-certificate-type"
	certificateVersion     = "\x00aram-certificate-version"
	certificateSigAlg      = "\x00aram-certificate-sigalg"
	certificateNotBefore   = "\x00aram-certificate-not-before"
	certificateNotAfter    = "\x00aram-certificate-not-after"
	certificateSerial      = "\x00aram-certificate-serial"
	certificateException   = "\x00aram-certificate-exception-certificate"
	certificateReason      = "\x00aram-certificate-exception-reason"
	pushRegistryStaticName = "\x00aram-push-registry"
	pushEntriesField       = "\x00aram-push-entries"
	pushConnectionField    = "\x00aram-push-connection"
	pushMIDletField        = "\x00aram-push-midlet"
	pushFilterField        = "\x00aram-push-filter"
	pushAvailableField     = "\x00aram-push-available"
	pushAlarmsField        = "\x00aram-push-alarms"
	pushAlarmMIDletField   = "\x00aram-push-alarm-midlet"
	pushAlarmTimeField     = "\x00aram-push-alarm-time"
)

func (vm *VM) installMIDPConnectionExtras() {
	vm.installMIDPHTTPNatives()
	vm.installMIDPSocketNatives()
	vm.installMIDPSecurityNatives()
	vm.installMIDPPushNatives()
	vm.installMIDPNetworkStaticFields()
}

func (vm *VM) installMIDPHTTPNatives() {
	for _, item := range []struct {
		name string
		part func(*url.URL) string
	}{
		{"getURL", func(value *url.URL) string { return value.String() }},
		{"getProtocol", func(value *url.URL) string { return value.Scheme }},
		{"getHost", func(value *url.URL) string { return value.Hostname() }},
		{"getFile", func(value *url.URL) string {
			if value.RawQuery != "" {
				return value.EscapedPath() + "?" + value.RawQuery
			}
			return value.EscapedPath()
		}},
		{"getRef", func(value *url.URL) string { return value.Fragment }},
		{"getQuery", func(value *url.URL) string { return value.RawQuery }},
	} {
		item := item
		vm.RegisterNative("javax/microedition/io/HttpConnection", item.name, "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			parsed, err := vm.httpURL(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value := item.part(parsed)
			if value == "" && (item.name == "getRef" || item.name == "getQuery") {
				return ReferenceValue(0), true, nil
			}
			return ReferenceValue(vm.NewString(value)), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getPort", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		parsed, err := vm.httpURL(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if parsed.Port() == "" {
			return IntValue(-1), true, nil
		}
		port, _ := strconv.ParseInt(parsed.Port(), 10, 32)
		return IntValue(int32(port)), true, nil
	})
	vm.RegisterNative("javax/microedition/io/HttpsConnection", "getPort", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		parsed, err := vm.httpURL(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if parsed.Port() == "" {
			return IntValue(443), true, nil
		}
		port, _ := strconv.ParseInt(parsed.Port(), 10, 32)
		return IntValue(int32(port)), true, nil
	})
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getRequestMethod", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.openHTTPConnection(receiver)
		if err != nil {
			return Value{}, false, err
		}
		request, err := vm.httpRequestSnapshot(state.request)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(request.Method)), true, nil
	})
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getRequestProperty", "(Ljava/lang/String;)Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		name, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		state, err := vm.openHTTPConnection(receiver)
		if err != nil {
			return Value{}, false, err
		}
		request, err := vm.httpRequestSnapshot(state.request)
		if err != nil {
			return Value{}, false, err
		}
		for _, property := range request.RequestHeaders {
			if strings.EqualFold(property.Name, name) {
				return ReferenceValue(vm.NewString(property.Value)), true, nil
			}
		}
		return ReferenceValue(0), true, nil
	})
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getResponseCode", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		request, err := vm.completedHTTPRequest(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(request.ResponseCode), true, nil
	})
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getResponseMessage", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		request, err := vm.completedHTTPRequest(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(http.StatusText(int(request.ResponseCode)))), true, nil
	})
	for _, item := range []struct{ name, header string }{{"getExpiration", "Expires"}, {"getDate", "Date"}, {"getLastModified", "Last-Modified"}} {
		item := item
		vm.RegisterNative("javax/microedition/io/HttpConnection", item.name, "()J", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			request, err := vm.completedHTTPRequest(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, ok := httpHeader(request.ResponseHeaders, item.header)
			if !ok {
				return LongValue(0), true, nil
			}
			parsed, parseErr := http.ParseTime(value)
			if parseErr != nil {
				return LongValue(0), true, nil
			}
			return LongValue(parsed.UnixMilli()), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getHeaderField", "(Ljava/lang/String;)Ljava/lang/String;", vm.nativeHTTPHeaderByName)
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getHeaderField", "(I)Ljava/lang/String;", vm.nativeHTTPHeaderByIndex)
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getHeaderFieldKey", "(I)Ljava/lang/String;", vm.nativeHTTPHeaderKey)
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getHeaderFieldInt", "(Ljava/lang/String;I)I", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value, _, err := vm.nativeHTTPHeaderByName(ctx, vm, receiver, args)
		if err != nil {
			return Value{}, false, err
		}
		fallback, _ := intArgument(args, 1)
		reference, _ := value.Reference()
		if reference == 0 {
			return IntValue(fallback), true, nil
		}
		text, _ := vm.String(reference)
		parsed, parseErr := strconv.ParseInt(strings.TrimSpace(text), 10, 32)
		if parseErr != nil {
			return IntValue(fallback), true, nil
		}
		return IntValue(int32(parsed)), true, nil
	})
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getHeaderFieldDate", "(Ljava/lang/String;J)J", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value, _, err := vm.nativeHTTPHeaderByName(ctx, vm, receiver, args)
		if err != nil {
			return Value{}, false, err
		}
		fallback, _ := args[1].Long()
		reference, _ := value.Reference()
		if reference == 0 {
			return LongValue(fallback), true, nil
		}
		text, _ := vm.String(reference)
		parsed, parseErr := http.ParseTime(text)
		if parseErr != nil {
			return LongValue(fallback), true, nil
		}
		return LongValue(parsed.UnixMilli()), true, nil
	})
}

func (vm *VM) completedHTTPRequest(receiver uint32) (shared.HTTPState, error) {
	state, err := vm.openHTTPConnection(receiver)
	if err != nil {
		return shared.HTTPState{}, err
	}
	if err = vm.ensureHTTPResponse(state); err != nil {
		return shared.HTTPState{}, err
	}
	return vm.httpRequestSnapshot(state.request)
}

func (vm *VM) httpURL(receiver uint32) (*url.URL, error) {
	state, err := vm.openHTTPConnection(receiver)
	if err != nil {
		return nil, err
	}
	request, err := vm.httpRequestSnapshot(state.request)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(request.URL)
	if err != nil {
		return nil, vm.newThrowable("java/io/IOException", "invalid HTTP URL")
	}
	return parsed, nil
}

func httpHeader(headers []shared.HTTPProperty, name string) (string, bool) {
	for _, property := range headers {
		if strings.EqualFold(property.Name, name) {
			return property.Value, true
		}
	}
	return "", false
}

func (vm *VM) nativeHTTPHeaderByName(_ context.Context, _ *VM, receiver uint32, args []Value) (Value, bool, error) {
	name, err := vm.stringArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	request, err := vm.completedHTTPRequest(receiver)
	if err != nil {
		return Value{}, false, err
	}
	value, ok := httpHeader(request.ResponseHeaders, name)
	if !ok {
		return ReferenceValue(0), true, nil
	}
	return ReferenceValue(vm.NewString(value)), true, nil
}

func (vm *VM) nativeHTTPHeaderByIndex(_ context.Context, _ *VM, receiver uint32, args []Value) (Value, bool, error) {
	index, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	request, err := vm.completedHTTPRequest(receiver)
	if err != nil {
		return Value{}, false, err
	}
	if index == 0 {
		status := fmt.Sprintf("HTTP/1.1 %d %s", request.ResponseCode, http.StatusText(int(request.ResponseCode)))
		return ReferenceValue(vm.NewString(strings.TrimSpace(status))), true, nil
	}
	if index < 1 || int(index) > len(request.ResponseHeaders) {
		return ReferenceValue(0), true, nil
	}
	return ReferenceValue(vm.NewString(request.ResponseHeaders[index-1].Value)), true, nil
}

func (vm *VM) nativeHTTPHeaderKey(_ context.Context, _ *VM, receiver uint32, args []Value) (Value, bool, error) {
	index, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	request, err := vm.completedHTTPRequest(receiver)
	if err != nil {
		return Value{}, false, err
	}
	if index < 1 || int(index) > len(request.ResponseHeaders) {
		return ReferenceValue(0), true, nil
	}
	return ReferenceValue(vm.NewString(request.ResponseHeaders[index-1].Name)), true, nil
}

func (vm *VM) installMIDPSocketNatives() {
	for _, item := range []struct {
		name       string
		descriptor string
		native     NativeFunc
	}{
		{"openInputStream", "()Ljava/io/InputStream;", nativeOpenConnectionInputStream},
		{"openOutputStream", "()Ljava/io/OutputStream;", nativeOpenConnectionOutputStream},
		{"close", "()V", nativeCloseConnection},
	} {
		vm.RegisterNative("javax/microedition/io/CommConnection", item.name, item.descriptor, item.native)
	}
	vm.RegisterNative("javax/microedition/io/CommConnection", "openDataInputStream", "()Ljava/io/DataInputStream;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if err := vm.ensureOpenConnection(receiver); err != nil {
			return Value{}, false, err
		}
		stream := vm.NewObject("java/io/InputStream", &inputStreamState{connection: receiver})
		return ReferenceValue(vm.NewObject("java/io/DataInputStream", &dataInputState{stream: stream})), true, nil
	})
	vm.RegisterNative("javax/microedition/io/CommConnection", "openDataOutputStream", "()Ljava/io/DataOutputStream;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if err := vm.ensureOpenConnection(receiver); err != nil {
			return Value{}, false, err
		}
		stream := vm.NewObject("java/io/OutputStream", &outputStreamState{connection: receiver})
		return ReferenceValue(vm.NewObject("java/io/DataOutputStream", &dataOutputState{stream: stream})), true, nil
	})
	vm.RegisterNative("javax/microedition/io/SocketConnection", "getAddress", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.openSocketConnection(receiver)
		if err != nil {
			return Value{}, false, err
		}
		info, err := vm.services.Network.SocketInfo(vm.serviceOwner, state.socket)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
		return ReferenceValue(vm.NewString(info.Host)), true, nil
	})
	vm.RegisterNative("javax/microedition/io/SocketConnection", "getPort", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.openSocketConnection(receiver)
		if err != nil {
			return Value{}, false, err
		}
		info, err := vm.services.Network.SocketInfo(vm.serviceOwner, state.socket)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
		return IntValue(int32(info.Port)), true, nil
	})
	for _, class := range []string{"javax/microedition/io/SocketConnection", "javax/microedition/io/UDPDatagramConnection", "javax/microedition/io/ServerSocketConnection"} {
		class := class
		vm.RegisterNative(class, "getLocalAddress", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if err := vm.ensureMIDPLocalConnectionOpen(receiver, class); err != nil {
				return Value{}, false, err
			}
			object, _ := vm.Object(receiver)
			value, ok := object.Fields[connectionLocalHostField]
			if !ok {
				value = ReferenceValue(vm.NewString("127.0.0.1"))
			}
			return value, true, nil
		})
		vm.RegisterNative(class, "getLocalPort", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if err := vm.ensureMIDPLocalConnectionOpen(receiver, class); err != nil {
				return Value{}, false, err
			}
			object, _ := vm.Object(receiver)
			if class == "javax/microedition/io/ServerSocketConnection" {
				return object.Fields[serverSocketPortField], true, nil
			}
			value, ok := object.Fields[connectionLocalPortField]
			if !ok {
				value = IntValue(0)
			}
			return value, true, nil
		})
	}
	vm.RegisterNative("javax/microedition/io/SocketConnection", "setSocketOption", "(BI)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		if _, err := vm.openSocketConnection(receiver); err != nil {
			return Value{}, false, err
		}
		option, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		value, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		if option < 0 || option > 4 || value < 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid socket option")
		}
		object, _ := vm.Object(receiver)
		optionsReference, _ := object.Fields[connectionOptionsField].Reference()
		options, ok := vm.Object(optionsReference)
		if !ok || options.Array == nil {
			return Value{}, false, fmt.Errorf("invalid socket options")
		}
		options.Array.Elements[option] = IntValue(value)
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/io/SocketConnection", "getSocketOption", "(B)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		if _, err := vm.openSocketConnection(receiver); err != nil {
			return Value{}, false, err
		}
		option, err := intArgument(args, 0)
		if err != nil || option < 0 || option > 4 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid socket option")
		}
		object, _ := vm.Object(receiver)
		optionsReference, _ := object.Fields[connectionOptionsField].Reference()
		options, ok := vm.Object(optionsReference)
		if !ok || options.Array == nil {
			return Value{}, false, fmt.Errorf("invalid socket options")
		}
		return options.Array.Elements[option], true, nil
	})
	vm.RegisterNative("javax/microedition/io/CommConnection", "getBaudRate", "()I", vm.nativeCommGetBaudRate)
	vm.RegisterNative("javax/microedition/io/CommConnection", "setBaudRate", "(I)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		if err := vm.ensureOpenConnection(receiver); err != nil {
			return Value{}, false, err
		}
		baud, err := intArgument(args, 0)
		if err != nil || baud <= 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid baud rate")
		}
		object, _ := vm.Object(receiver)
		object.Fields[commBaudField] = IntValue(baud)
		return IntValue(baud), true, nil
	})
}

func (vm *VM) nativeCommGetBaudRate(_ context.Context, _ *VM, receiver uint32, _ []Value) (Value, bool, error) {
	if err := vm.ensureOpenConnection(receiver); err != nil {
		return Value{}, false, err
	}
	object, _ := vm.Object(receiver)
	return object.Fields[commBaudField], true, nil
}

func (vm *VM) ensureMIDPLocalConnectionOpen(reference uint32, class string) error {
	object, ok := vm.Object(reference)
	if !ok {
		return fmt.Errorf("invalid connection reference %d", reference)
	}
	if class == "javax/microedition/io/ServerSocketConnection" {
		closed, _ := object.Fields[serverSocketClosedField].Int()
		if closed != 0 {
			return vm.newThrowable("java/io/IOException", "connection closed")
		}
		return nil
	}
	_, err := vm.openSocketConnection(reference)
	return err
}

func (vm *VM) installMIDPSecurityNatives() {
	for _, class := range []string{"javax/microedition/io/HttpsConnection", "javax/microedition/io/SecureConnection"} {
		class := class
		vm.RegisterNative(class, "getSecurityInfo", "()Ljavax/microedition/io/SecurityInfo;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if class == "javax/microedition/io/HttpsConnection" {
				if _, err := vm.completedHTTPRequest(receiver); err != nil {
					return Value{}, false, err
				}
			} else if _, err := vm.openSocketConnection(receiver); err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.newSecurityInfo(receiver)), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/io/SecurityInfo", "getServerCertificate", "()Ljavax/microedition/pki/Certificate;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid SecurityInfo")
		}
		return object.Fields[securityCertificate], true, nil
	})
	for _, item := range []struct{ name, value string }{{"getProtocolVersion", "3.1"}, {"getProtocolName", "TLS"}, {"getCipherSuite", "TLS_RSA_WITH_AES_128_CBC_SHA"}} {
		item := item
		vm.RegisterNative("javax/microedition/io/SecurityInfo", item.name, "()Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(vm.NewString(item.value)), true, nil
		})
	}
	for _, item := range []struct{ name, field string }{{"getSubject", certificateSubject}, {"getIssuer", certificateIssuer}, {"getType", certificateType}, {"getVersion", certificateVersion}, {"getSigAlgName", certificateSigAlg}, {"getSerialNumber", certificateSerial}} {
		item := item
		vm.RegisterNative("javax/microedition/pki/Certificate", item.name, "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Certificate")
			}
			return object.Fields[item.field], true, nil
		})
	}
	for _, item := range []struct{ name, field string }{{"getNotBefore", certificateNotBefore}, {"getNotAfter", certificateNotAfter}} {
		item := item
		vm.RegisterNative("javax/microedition/pki/Certificate", item.name, "()J", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Certificate")
			}
			return object.Fields[item.field], true, nil
		})
	}
	vm.installCertificateExceptionNatives()
}

func (vm *VM) newSecurityInfo(connection uint32) uint32 {
	subject := "CN=localhost"
	if parsed, err := vm.httpURL(connection); err == nil && parsed.Hostname() != "" {
		subject = "CN=" + parsed.Hostname()
	} else if state, err := vm.openSocketConnection(connection); err == nil {
		if info, infoErr := vm.services.Network.SocketInfo(vm.serviceOwner, state.socket); infoErr == nil && info.Host != "" {
			subject = "CN=" + info.Host
		}
	}
	now := time.UnixMilli(vm.services.Clock.WallMillis())
	certificate := vm.NewObject("javax/microedition/pki/Certificate", nil)
	object, _ := vm.Object(certificate)
	for field, value := range map[string]Value{
		certificateSubject:   ReferenceValue(vm.NewString(subject)),
		certificateIssuer:    ReferenceValue(vm.NewString("CN=aram-core deterministic CA")),
		certificateType:      ReferenceValue(vm.NewString("X.509")),
		certificateVersion:   ReferenceValue(vm.NewString("3")),
		certificateSigAlg:    ReferenceValue(vm.NewString("SHA256withRSA")),
		certificateNotBefore: LongValue(now.Add(-24 * time.Hour).UnixMilli()),
		certificateNotAfter:  LongValue(now.Add(365 * 24 * time.Hour).UnixMilli()),
		certificateSerial:    ReferenceValue(vm.NewString("00")),
	} {
		object.Fields[field] = value
	}
	security := vm.NewObject("javax/microedition/io/SecurityInfo", nil)
	securityObject, _ := vm.Object(security)
	securityObject.Fields[securityCertificate] = ReferenceValue(certificate)
	return security
}

func (vm *VM) installCertificateExceptionNatives() {
	for _, descriptor := range []string{"(Ljavax/microedition/pki/Certificate;B)V", "(Ljava/lang/String;Ljavax/microedition/pki/Certificate;B)V"} {
		descriptor := descriptor
		vm.RegisterNative("javax/microedition/pki/CertificateException", "<init>", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			argument := 0
			if strings.HasPrefix(descriptor, "(Ljava/lang/String;") {
				message, err := vm.stringArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				if err = vm.setNative(receiver, message); err != nil {
					return Value{}, false, err
				}
				argument++
			}
			certificate, err := referenceArgument(args, argument)
			if err != nil {
				return Value{}, false, err
			}
			reason, err := intArgument(args, argument+1)
			if err != nil || reason < 1 || reason > 14 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid certificate reason")
			}
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid CertificateException")
			}
			object.Fields[certificateException] = ReferenceValue(certificate)
			object.Fields[certificateReason] = IntValue(reason)
			return Value{}, false, nil
		})
	}
	vm.RegisterNative("javax/microedition/pki/CertificateException", "getCertificate", "()Ljavax/microedition/pki/Certificate;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid CertificateException")
		}
		return object.Fields[certificateException], true, nil
	})
	vm.RegisterNative("javax/microedition/pki/CertificateException", "getReason", "()B", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid CertificateException")
		}
		return object.Fields[certificateReason], true, nil
	})
}

func (vm *VM) installMIDPPushNatives() {
	vm.RegisterNative("javax/microedition/io/PushRegistry", "registerConnection", "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)V", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		connection, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		midlet, err := vm.stringArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		filter, err := vm.stringArgument(args, 2)
		if err != nil {
			return Value{}, false, err
		}
		if strings.TrimSpace(connection) == "" || strings.TrimSpace(midlet) == "" || strings.TrimSpace(filter) == "" {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid push registration")
		}
		entries := vm.pushArray(pushEntriesField, "[Ljava/lang/Object;")
		if vm.findPushEntry(entries, connection) >= 0 {
			return Value{}, false, vm.newThrowable("java/io/IOException", "connection already registered")
		}
		entry := vm.NewObject("java/lang/Object", nil)
		object, _ := vm.Object(entry)
		object.Fields[pushConnectionField] = ReferenceValue(vm.NewString(connection))
		object.Fields[pushMIDletField] = ReferenceValue(vm.NewString(midlet))
		object.Fields[pushFilterField] = ReferenceValue(vm.NewString(filter))
		object.Fields[pushAvailableField] = IntValue(0)
		entries.Elements = append(entries.Elements, ReferenceValue(entry))
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/io/PushRegistry", "unregisterConnection", "(Ljava/lang/String;)Z", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		connection, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		entries := vm.pushArray(pushEntriesField, "[Ljava/lang/Object;")
		index := vm.findPushEntry(entries, connection)
		if index < 0 {
			return IntValue(0), true, nil
		}
		entries.Elements = append(entries.Elements[:index], entries.Elements[index+1:]...)
		return IntValue(1), true, nil
	})
	vm.RegisterNative("javax/microedition/io/PushRegistry", "listConnections", "(Z)[Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		available, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		entries := vm.pushArray(pushEntriesField, "[Ljava/lang/Object;")
		values := make([]Value, 0, len(entries.Elements))
		for _, entryValue := range entries.Elements {
			entry, _ := entryValue.Reference()
			object, _ := vm.Object(entry)
			ready, _ := object.Fields[pushAvailableField].Int()
			if available == 0 || ready != 0 {
				values = append(values, object.Fields[pushConnectionField])
			}
		}
		return ReferenceValue(vm.newArray("[Ljava/lang/String;", values)), true, nil
	})
	for _, item := range []struct{ name, field string }{{"getMIDlet", pushMIDletField}, {"getFilter", pushFilterField}} {
		item := item
		vm.RegisterNative("javax/microedition/io/PushRegistry", item.name, "(Ljava/lang/String;)Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			connection, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			entries := vm.pushArray(pushEntriesField, "[Ljava/lang/Object;")
			index := vm.findPushEntry(entries, connection)
			if index < 0 {
				return ReferenceValue(0), true, nil
			}
			entry, _ := entries.Elements[index].Reference()
			object, _ := vm.Object(entry)
			return object.Fields[item.field], true, nil
		})
	}
	vm.RegisterNative("javax/microedition/io/PushRegistry", "registerAlarm", "(Ljava/lang/String;J)J", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		midlet, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		at, err := args[1].Long()
		if err != nil || strings.TrimSpace(midlet) == "" || at < 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid alarm")
		}
		alarms := vm.pushArray(pushAlarmsField, "[Ljava/lang/Object;")
		previous := int64(0)
		for _, alarmValue := range alarms.Elements {
			alarm, _ := alarmValue.Reference()
			object, _ := vm.Object(alarm)
			nameReference, _ := object.Fields[pushAlarmMIDletField].Reference()
			name, _ := vm.String(nameReference)
			if name == midlet {
				previous, _ = object.Fields[pushAlarmTimeField].Long()
				object.Fields[pushAlarmTimeField] = LongValue(at)
				return LongValue(previous), true, nil
			}
		}
		alarm := vm.NewObject("java/lang/Object", nil)
		object, _ := vm.Object(alarm)
		object.Fields[pushAlarmMIDletField] = ReferenceValue(vm.NewString(midlet))
		object.Fields[pushAlarmTimeField] = LongValue(at)
		alarms.Elements = append(alarms.Elements, ReferenceValue(alarm))
		return LongValue(0), true, nil
	})
}

func (vm *VM) pushArray(field, descriptor string) *Array {
	key := fieldStorageKey("javax/microedition/io/PushRegistry", pushRegistryStaticName, "Ljava/lang/Object;")
	registryReference, _ := vm.hostStatic[key].Reference()
	registry, _ := vm.Object(registryReference)
	arrayReference, _ := registry.Fields[field].Reference()
	arrayObject, _ := vm.Object(arrayReference)
	if arrayObject == nil || arrayObject.Array == nil {
		arrayReference = vm.newArray(descriptor, nil)
		registry.Fields[field] = ReferenceValue(arrayReference)
		arrayObject, _ = vm.Object(arrayReference)
	}
	return arrayObject.Array
}

func (vm *VM) findPushEntry(entries *Array, connection string) int {
	for index, entryValue := range entries.Elements {
		entry, _ := entryValue.Reference()
		object, _ := vm.Object(entry)
		nameReference, _ := object.Fields[pushConnectionField].Reference()
		name, _ := vm.String(nameReference)
		if name == connection {
			return index
		}
	}
	return -1
}

func (vm *VM) installMIDPNetworkStaticFields() {
	for name, value := range map[string]int32{"DELAY": 0, "LINGER": 1, "KEEPALIVE": 2, "RCVBUF": 3, "SNDBUF": 4} {
		vm.RegisterStaticField("javax/microedition/io/SocketConnection", name, "B", IntValue(value))
	}
	for name, value := range map[string]string{"HEAD": "HEAD", "GET": "GET", "POST": "POST"} {
		vm.RegisterStaticField("javax/microedition/io/HttpConnection", name, "Ljava/lang/String;", ReferenceValue(vm.NewString(value)))
	}
	for name, value := range map[string]int32{
		"HTTP_OK": 200, "HTTP_CREATED": 201, "HTTP_ACCEPTED": 202, "HTTP_NOT_AUTHORITATIVE": 203,
		"HTTP_NO_CONTENT": 204, "HTTP_RESET": 205, "HTTP_PARTIAL": 206, "HTTP_MULT_CHOICE": 300,
		"HTTP_MOVED_PERM": 301, "HTTP_MOVED_TEMP": 302, "HTTP_SEE_OTHER": 303, "HTTP_NOT_MODIFIED": 304,
		"HTTP_USE_PROXY": 305, "HTTP_TEMP_REDIRECT": 307, "HTTP_BAD_REQUEST": 400, "HTTP_UNAUTHORIZED": 401,
		"HTTP_PAYMENT_REQUIRED": 402, "HTTP_FORBIDDEN": 403, "HTTP_NOT_FOUND": 404, "HTTP_BAD_METHOD": 405,
		"HTTP_NOT_ACCEPTABLE": 406, "HTTP_PROXY_AUTH": 407, "HTTP_CLIENT_TIMEOUT": 408, "HTTP_CONFLICT": 409,
		"HTTP_GONE": 410, "HTTP_LENGTH_REQUIRED": 411, "HTTP_PRECON_FAILED": 412, "HTTP_ENTITY_TOO_LARGE": 413,
		"HTTP_REQ_TOO_LONG": 414, "HTTP_UNSUPPORTED_TYPE": 415, "HTTP_UNSUPPORTED_RANGE": 416, "HTTP_EXPECT_FAILED": 417,
		"HTTP_INTERNAL_ERROR": 500, "HTTP_NOT_IMPLEMENTED": 501, "HTTP_BAD_GATEWAY": 502, "HTTP_UNAVAILABLE": 503,
		"HTTP_GATEWAY_TIMEOUT": 504, "HTTP_VERSION": 505,
	} {
		vm.RegisterStaticField("javax/microedition/io/HttpConnection", name, "I", IntValue(value))
	}
	for name, value := range map[string]int32{
		"BAD_EXTENSIONS": 1, "CERTIFICATE_CHAIN_TOO_LONG": 2, "EXPIRED": 3, "UNAUTHORIZED_INTERMEDIATE_CA": 4,
		"MISSING_SIGNATURE": 5, "NOT_YET_VALID": 6, "SITENAME_MISMATCH": 7, "UNRECOGNIZED_ISSUER": 8,
		"UNSUPPORTED_SIGALG": 9, "INAPPROPRIATE_KEY_USAGE": 10, "BROKEN_CHAIN": 11, "ROOT_CA_EXPIRED": 12,
		"UNSUPPORTED_PUBLIC_KEY_TYPE": 13, "VERIFICATION_FAILED": 14,
	} {
		vm.RegisterStaticField("javax/microedition/pki/CertificateException", name, "B", IntValue(value))
	}
	registry := vm.NewObject("java/lang/Object", nil)
	registryObject, _ := vm.Object(registry)
	registryObject.Fields[pushEntriesField] = ReferenceValue(vm.newArray("[Ljava/lang/Object;", nil))
	registryObject.Fields[pushAlarmsField] = ReferenceValue(vm.newArray("[Ljava/lang/Object;", nil))
	vm.RegisterStaticField("javax/microedition/io/PushRegistry", pushRegistryStaticName, "Ljava/lang/Object;", ReferenceValue(registry))
}
