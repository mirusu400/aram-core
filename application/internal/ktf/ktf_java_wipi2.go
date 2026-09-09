package ktf

import (
	"sort"
	"strings"
)

const ktfWIPI2SMSMaxLength = 140

type ktfWIPI2IODevice struct {
	name   string
	number int
	data   []byte
	closed bool
}

type ktfWIPI2ResourceGroup struct {
	name       string
	lockStatus int
}

type ktfWIPI2Resource struct {
	id         string
	title      string
	uiName     string
	format     string
	state      string
	data       []byte
	lockStatus int
}

type ktfWIPI2IODeviceSnapshot struct {
	Name   string
	Number int32
	Data   []byte
	Closed bool
}

type ktfWIPI2ResourceGroupSnapshot struct {
	Name       string
	LockStatus int32
}

type ktfWIPI2ResourceSnapshot struct {
	ID         string
	Title      string
	UIName     string
	Format     string
	State      string
	Data       []byte
	LockStatus int32
}

func snapshotWIPI2IODevices(source map[uint32]*ktfWIPI2IODevice) map[uint32]ktfWIPI2IODeviceSnapshot {
	result := make(map[uint32]ktfWIPI2IODeviceSnapshot, len(source))
	for instance, device := range source {
		if device == nil {
			continue
		}
		result[instance] = ktfWIPI2IODeviceSnapshot{
			Name: device.name, Number: int32(device.number),
			Data: append([]byte(nil), device.data...), Closed: device.closed,
		}
	}
	return result
}

func restoreWIPI2IODevices(source map[uint32]ktfWIPI2IODeviceSnapshot) map[uint32]*ktfWIPI2IODevice {
	result := make(map[uint32]*ktfWIPI2IODevice, len(source))
	for instance, device := range source {
		result[instance] = &ktfWIPI2IODevice{
			name: device.Name, number: int(device.Number),
			data: append([]byte(nil), device.Data...), closed: device.Closed,
		}
	}
	return result
}

func snapshotWIPI2ResourceGroups(source map[uint32]*ktfWIPI2ResourceGroup) map[uint32]ktfWIPI2ResourceGroupSnapshot {
	result := make(map[uint32]ktfWIPI2ResourceGroupSnapshot, len(source))
	for instance, group := range source {
		if group != nil {
			result[instance] = ktfWIPI2ResourceGroupSnapshot{
				Name: group.name, LockStatus: int32(group.lockStatus),
			}
		}
	}
	return result
}

func restoreWIPI2ResourceGroups(source map[uint32]ktfWIPI2ResourceGroupSnapshot) map[uint32]*ktfWIPI2ResourceGroup {
	result := make(map[uint32]*ktfWIPI2ResourceGroup, len(source))
	for instance, group := range source {
		result[instance] = &ktfWIPI2ResourceGroup{
			name: group.Name, lockStatus: int(group.LockStatus),
		}
	}
	return result
}

func snapshotWIPI2Resources(source map[string]map[string]*ktfWIPI2Resource) map[string]map[string]ktfWIPI2ResourceSnapshot {
	result := make(map[string]map[string]ktfWIPI2ResourceSnapshot, len(source))
	for groupName, resources := range source {
		group := make(map[string]ktfWIPI2ResourceSnapshot, len(resources))
		for name, resource := range resources {
			if resource == nil {
				continue
			}
			group[name] = ktfWIPI2ResourceSnapshot{
				ID: resource.id, Title: resource.title,
				UIName: resource.uiName, Format: resource.format,
				State: resource.state, Data: append([]byte(nil), resource.data...),
				LockStatus: int32(resource.lockStatus),
			}
		}
		result[groupName] = group
	}
	return result
}

func restoreWIPI2Resources(source map[string]map[string]ktfWIPI2ResourceSnapshot) map[string]map[string]*ktfWIPI2Resource {
	result := make(map[string]map[string]*ktfWIPI2Resource, len(source))
	for groupName, resources := range source {
		group := make(map[string]*ktfWIPI2Resource, len(resources))
		for name, resource := range resources {
			group[name] = &ktfWIPI2Resource{
				id: resource.ID, title: resource.Title,
				uiName: resource.UIName, format: resource.Format,
				state: resource.State, data: append([]byte(nil), resource.Data...),
				lockStatus: int(resource.LockStatus),
			}
		}
		result[groupName] = group
	}
	return result
}

func init() {
	HostJavaClassSpecs["org/kwis/msp/lwc/ConstraintChecker"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
	}
	kernelSpec := HostJavaClassSpecs["org/kwis/msf/core/Kernel"]
	kernelSpec.methods = append(kernelSpec.methods,
		ktfCompatibilityMethod("load", "(Ljava/lang/String;)I", 0x0008),
	)
	HostJavaClassSpecs["org/kwis/msf/core/Kernel"] = kernelSpec
	ledSpec := HostJavaClassSpecs["org/kwis/msp/handset/LED"]
	ledSpec.methods = append(ledSpec.methods,
		ktfCompatibilityMethod("<init>", "()V"),
		ktfCompatibilityMethod("set", "(I)I", 0x0008),
		ktfCompatibilityMethod("getSupportColor", "(I)[I", 0x0008),
		ktfCompatibilityMethod("getColor", "(I)I", 0x0008),
		ktfCompatibilityMethod("setColor", "(II)I", 0x0008),
	)
	HostJavaClassSpecs["org/kwis/msp/handset/LED"] = ledSpec
	HostJavaClassSpecs["org/kwis/msp/lcdui/AnimateImage"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "createAnimateImage", descriptor: "(Ljava/lang/String;Z)Lorg/kwis/msp/lcdui/AnimateImage;", access: 0x0008},
			{name: "createAnimateImage", descriptor: "(III)Lorg/kwis/msp/lcdui/AnimateImage;", access: 0x0008},
			{name: "isMutable", descriptor: "()Z"},
			{name: "setAnimationRate", descriptor: "(II)V"},
			{name: "getAnimationRate", descriptor: "()I"},
			{name: "getAnimationRate", descriptor: "(I)I"},
			{name: "getMaxFrame", descriptor: "()I"},
			{name: "getWidth", descriptor: "()I"},
			{name: "getHeight", descriptor: "()I"},
			{name: "getFrameImage", descriptor: "(I)Lorg/kwis/msp/lcdui/Image;"},
			{name: "setFrameImage", descriptor: "(Lorg/kwis/msp/lcdui/Image;I)V"},
			{name: "play", descriptor: "(Lorg/kwis/msp/lcdui/Graphics;IIILorg/kwis/msp/lcdui/ImageObserver;)V"},
			{name: "play", descriptor: "(Lorg/kwis/msp/lcdui/ImageObserver;)V"},
			{name: "stop", descriptor: "()V"},
			{name: "stopImage", descriptor: "(Lorg/kwis/msp/lcdui/ImageObserver;)V", access: 0x0008},
			{name: "paintFrame", descriptor: "(Lorg/kwis/msp/lcdui/Graphics;III)Z"},
			{name: "paintFrame", descriptor: "(Lorg/kwis/msp/lcdui/Graphics;III)V"},
			{name: "setRepeat", descriptor: "(Ljava/lang/Boolean;)Z"},
			{name: "setRepeat", descriptor: "(Z)Z"},
			{name: "isRepeat", descriptor: "()Z"},
			{name: "getImageType", descriptor: "()Ljava/lang/String;"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/media/BaseClip"] = ktfHostJavaClassSpec{
		Parent:              "java/lang/Object",
		access:              0x0401,
		compatibilityVTable: true,
		methods: []ktfHostJavaMethodSpec{
			{name: "allocPlayer", descriptor: "()I", access: 0x0004},
			{name: "freePlayer", descriptor: "()I", access: 0x0004},
			{name: "free", descriptor: "()V"},
			{name: "mediaWriteData", descriptor: "()I", access: 0x0004},
			{name: "mediaReadData", descriptor: "()I", access: 0x0004},
			{name: "setWaterMark", descriptor: "(I)V"},
			{name: "reactiveWaterMark", descriptor: "()V"},
			{name: "setBuffer", descriptor: "([BI)Z"},
			{name: "putData", descriptor: "([BII)I"},
			{name: "getData", descriptor: "([BII)I"},
			{name: "clearData", descriptor: "()V"},
			{name: "availableDataSize", descriptor: "()I"},
			{name: "playStart", descriptor: "(Z)Z", access: 0x0004},
			{name: "recordStart", descriptor: "()Z", access: 0x0004},
			{name: "playUpdate", descriptor: "(II)Z"},
			{name: "mediaControl", descriptor: "(I[I[I)I", access: 0x0004},
			{name: "mediaControl", descriptor: "(I[I[B)I", access: 0x0004},
			{name: "mediaControl", descriptor: "(ILjava/lang/String;[I)I", access: 0x0004},
			{name: "mediaModeControl", descriptor: "(Ljava/lang/String;II[I)I"},
		},
	}
	clipSpec := HostJavaClassSpecs["org/kwis/msp/media/Clip"]
	clipSpec.Parent = "org/kwis/msp/media/BaseClip"
	HostJavaClassSpecs["org/kwis/msp/media/Clip"] = clipSpec
	playerSpec := HostJavaClassSpecs["org/kwis/msp/media/Player"]
	playerSpec.methods = append(playerSpec.methods,
		ktfCompatibilityMethod("play", "(Lorg/kwis/msp/media/BaseClip;Z)Z", 0x0008),
		ktfCompatibilityMethod("stop", "(Lorg/kwis/msp/media/BaseClip;)Z", 0x0008),
		ktfCompatibilityMethod("pause", "(Lorg/kwis/msp/media/BaseClip;)Z", 0x0008),
		ktfCompatibilityMethod("resume", "(Lorg/kwis/msp/media/BaseClip;)Z", 0x0008),
		ktfCompatibilityMethod("record", "(Lorg/kwis/msp/media/BaseClip;)Z", 0x0008),
	)
	HostJavaClassSpecs["org/kwis/msp/media/Player"] = playerSpec
	fileSpec := HostJavaClassSpecs["org/kwis/msp/io/File"]
	fileSpec.fields = append(fileSpec.fields,
		ktfHostJavaFieldSpec{name: "maxInputStream", descriptor: "I"},
		ktfHostJavaFieldSpec{name: "maxOutputStream", descriptor: "I"},
	)
	fileSpec.methods = append(fileSpec.methods,
		ktfHostJavaMethodSpec{name: "read", descriptor: "()I"},
		ktfHostJavaMethodSpec{name: "tell", descriptor: "()I"},
	)
	HostJavaClassSpecs["org/kwis/msp/io/File"] = fileSpec
	messageSpec := HostJavaClassSpecs["org/kwis/msf/io/Message"]
	messageSpec.methods = append(messageSpec.methods,
		// WIPI 2.0 printed these setter overloads with a get prefix. Real
		// handsets accepted both spellings, so retain the document ABI too.
		ktfHostJavaMethodSpec{name: "getAddressInt", descriptor: "(I)V"},
		ktfHostJavaMethodSpec{name: "getDate", descriptor: "(Ljava/util/Date;)V"},
	)
	HostJavaClassSpecs["org/kwis/msf/io/Message"] = messageSpec
	graphicsSpec := HostJavaClassSpecs["org/kwis/msp/lcdui/Graphics"]
	graphicsSpec.methods = append(graphicsSpec.methods,
		ktfHostJavaMethodSpec{name: "fillPolygon", descriptor: "([I[I)V"},
		ktfHostJavaMethodSpec{name: "reset", descriptor: "()V"},
		ktfHostJavaMethodSpec{name: "setPixels", descriptor: "(IIII[BII)V"},
	)
	HostJavaClassSpecs["org/kwis/msp/lcdui/Graphics"] = graphicsSpec
	componentSpec := HostJavaClassSpecs["org/kwis/msp/lwc/Component"]
	componentSpec.methods = append(componentSpec.methods,
		ktfHostJavaMethodSpec{name: "keyNotify", descriptor: "(II)Z", access: 0x0004},
		ktfHostJavaMethodSpec{name: "layout", descriptor: "()V", access: 0x0004},
		ktfHostJavaMethodSpec{name: "paintContent", descriptor: "(Lorg/kwis/msp/lcdui/Graphics;)V"},
		ktfHostJavaMethodSpec{name: "pointerNotify", descriptor: "(III)Z", access: 0x0004},
		ktfHostJavaMethodSpec{name: "processEvent", descriptor: "(IIII)Z", access: 0x0004},
		ktfHostJavaMethodSpec{name: "repaint", descriptor: "()V"},
		ktfHostJavaMethodSpec{name: "repaint", descriptor: "(IIII)V"},
		ktfHostJavaMethodSpec{name: "serviceRepaints", descriptor: "()V"},
		ktfHostJavaMethodSpec{name: "setBackground", descriptor: "(I)V"},
		ktfHostJavaMethodSpec{name: "setEventListener", descriptor: "(Lorg/kwis/msp/lwc/EventListener;Ljava/lang/Object;)V"},
		ktfHostJavaMethodSpec{name: "setFocus", descriptor: "()V"},
		ktfHostJavaMethodSpec{name: "setForeground", descriptor: "(I)V"},
		ktfHostJavaMethodSpec{name: "showNotify", descriptor: "(Z)V", access: 0x0004},
		ktfHostJavaMethodSpec{name: "validate", descriptor: "()V"},
	)
	HostJavaClassSpecs["org/kwis/msp/lwc/Component"] = componentSpec
	listSpec := HostJavaClassSpecs["org/kwis/msp/lwc/ListComponent"]
	listSpec.methods = append(listSpec.methods,
		ktfHostJavaMethodSpec{name: "getSelectedIndexs", descriptor: "()[I"},
		ktfHostJavaMethodSpec{name: "getString", descriptor: "(I)Ljava/lang/String;"},
		ktfHostJavaMethodSpec{name: "insert", descriptor: "(ILjava/lang/String;Lorg/kwis/msp/lcdui/Image;)I"},
		ktfHostJavaMethodSpec{name: "isControlNumber", descriptor: "()Z"},
		ktfHostJavaMethodSpec{name: "isSelected", descriptor: "(I)Z"},
		ktfHostJavaMethodSpec{name: "select", descriptor: "(Lorg/kwis/msp/lwc/ListItemComponent;)V"},
		ktfHostJavaMethodSpec{name: "set", descriptor: "(ILjava/lang/String;Lorg/kwis/msp/lcdui/Image;)V"},
	)
	HostJavaClassSpecs["org/kwis/msp/lwc/ListComponent"] = listSpec
	volumeSpec := HostJavaClassSpecs["org/kwis/msp/media/Volume"]
	volumeSpec.methods = append(volumeSpec.methods,
		ktfHostJavaMethodSpec{name: "getMute", descriptor: "(I)Z", access: 0x0008},
		ktfHostJavaMethodSpec{name: "setMute", descriptor: "(IZ)V", access: 0x0008},
		ktfHostJavaMethodSpec{name: "setMuteState", descriptor: "(IZ)V", access: 0x0008},
		ktfHostJavaMethodSpec{name: "getDefaultVolume", descriptor: "(I)I", access: 0x0008},
		ktfHostJavaMethodSpec{name: "setDefaultVolume", descriptor: "(II)V", access: 0x0008},
	)
	HostJavaClassSpecs["org/kwis/msp/media/Volume"] = volumeSpec
	HostJavaClassSpecs["org/kwis/msp/media/PlayerListener"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		access: 0x0601,
		methods: []ktfHostJavaMethodSpec{
			{name: "playerUpdate", descriptor: "(Lorg/kwis/msp/media/Clip;II)V", access: 0x0401},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/media/UnavailableException"] = ktfHostJavaClassSpec{
		Parent:  "java/lang/RuntimeException",
		methods: ktfThrowableSubclassSpec("java/lang/RuntimeException").methods,
	}
	HostJavaClassSpecs["org/kwis/msp/media/Camera"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/lang/String;)V", access: 0x0004},
			{name: "<init>", descriptor: "(Ljava/lang/String;I)V", access: 0x0004},
			{name: "detect", descriptor: "()Z", access: 0x0008},
			{name: "getModel", descriptor: "()Ljava/lang/String;", access: 0x0008},
			{name: "setMode", descriptor: "(I)Z"},
			{name: "getModeCount", descriptor: "()I"},
			{name: "setProperty", descriptor: "(I)Z"},
			{name: "setSize", descriptor: "(IIII)Z"},
			{name: "enableOEMDisplayArea", descriptor: "()V"},
			{name: "disableOEMDisplayArea", descriptor: "()V"},
			{name: "previewStart", descriptor: "()V"},
			{name: "previewStop", descriptor: "()V"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/media/StillClip"] = ktfHostJavaClassSpec{
		Parent: "org/kwis/msp/media/Camera",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/lang/String;)V", access: 0x0004},
			{name: "<init>", descriptor: "(Ljava/lang/String;I)V", access: 0x0004},
			{name: "snapshot", descriptor: "(Lorg/kwis/msp/media/PlayListener;)Z"},
			{name: "view", descriptor: "(Lorg/kwis/msp/media/PlayListener;)Z"},
			{name: "getData", descriptor: "()[B"},
			{name: "playStart", descriptor: "(Z)Z", access: 0x0004},
			{name: "playUpdate", descriptor: "(II)Z"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/media/VideoClip"] = ktfHostJavaClassSpec{
		Parent: "org/kwis/msp/media/Camera",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/lang/String;)V", access: 0x0004},
			{name: "<init>", descriptor: "(Ljava/lang/String;I)V", access: 0x0004},
			{name: "record", descriptor: "(Lorg/kwis/msp/media/PlayListener;)Z"},
			{name: "pause", descriptor: "()Z"},
			{name: "resume", descriptor: "()Z"},
			{name: "stop", descriptor: "()Z"},
			{name: "play", descriptor: "(Lorg/kwis/msp/media/PlayListener;)Z"},
			{name: "playStart", descriptor: "(Z)Z", access: 0x0004},
			{name: "playUpdate", descriptor: "(II)Z"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/handset/Address"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "getRecordId", descriptor: "()I"},
			{name: "getField", descriptor: "(I)Ljava/lang/Object;"},
			{name: "setField", descriptor: "(ILjava/lang/Object;)Z"},
			{name: "getFields", descriptor: "()[Ljava/lang/Object;"},
			{name: "setFields", descriptor: "([Ljava/lang/Object;)Z"},
			{name: "getLockStatus", descriptor: "()I"},
			{name: "setLockStatus", descriptor: "(I)I"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/handset/AddressBook"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "getAddressBook", descriptor: "()Lorg/kwis/msp/handset/AddressBook;", access: 0x0008},
			{name: "getGroupCount", descriptor: "()I"},
			{name: "getGroupName", descriptor: "(I)Ljava/lang/String;"},
			{name: "createGroup", descriptor: "(Ljava/lang/String;)I"},
			{name: "getFieldCount", descriptor: "()I"},
			{name: "getFieldName", descriptor: "(I)Ljava/lang/String;"},
			{name: "getFieldType", descriptor: "(I)I"},
			{name: "getFieldMaxLength", descriptor: "(I)I"},
			{name: "getAddressMaxCount", descriptor: "()I"},
			{name: "getAddressCount", descriptor: "()I"},
			{name: "getAddressRecordIdsAll", descriptor: "()[I"},
			{name: "getAddress", descriptor: "(I)Lorg/kwis/msp/handset/Address;"},
			{name: "createRecord", descriptor: "([Ljava/lang/Object;)I"},
			{name: "createRecords", descriptor: "([Ljava/lang/Object;)[I"},
			{name: "isSupportFieldShortCut", descriptor: "()Z"},
			{name: "isSupportShortCut", descriptor: "(I)Z"},
			{name: "getMaxShortCut", descriptor: "()I"},
			{name: "getFirstFreeShortCut", descriptor: "(I)I"},
			{name: "setShortCut", descriptor: "(III)Z"},
			{name: "setShortCut", descriptor: "([I[I[I)Z"},
			{name: "getAllShortCut", descriptor: "()[I"},
			{name: "getShortCutItem", descriptor: "(I)[I"},
			{name: "getShortCutAssigned", descriptor: "(II)I"},
			{name: "searchAddress", descriptor: "(ILjava/lang/Object;Z)[I"},
			{name: "removeAddress", descriptor: "(I)Z"},
			{name: "checkPassword", descriptor: "(ILjava/lang/String;)I"},
			{name: "getLockStatus", descriptor: "()I"},
			{name: "setLockStatus", descriptor: "(I)I"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/handset/StationLocationInfo"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "getBaseID", descriptor: "()I"},
			{name: "getBaseLat", descriptor: "()I"},
			{name: "getBaseLong", descriptor: "()I"},
			{name: "isValid", descriptor: "()Z"},
			{name: "getLocationInfo", descriptor: "()I"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/handset/GPSConfig"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(IIIIII)V"},
			{name: "getMode", descriptor: "()I"},
			{name: "getOptimization", descriptor: "()I"},
			{name: "getQos", descriptor: "()I"},
			{name: "getTransport", descriptor: "()I"},
			{name: "getPdeAddr", descriptor: "()I"},
			{name: "getPdePort", descriptor: "()I"},
			{name: "setMode", descriptor: "(I)I", access: 0x0008},
			{name: "setOptimization", descriptor: "(I)I", access: 0x0008},
			{name: "setQos", descriptor: "(I)I", access: 0x0008},
			{name: "setTransport", descriptor: "(I)I", access: 0x0008},
			{name: "setPdeAddr", descriptor: "(I)I", access: 0x0008},
			{name: "setPdePort", descriptor: "(I)I", access: 0x0008},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/handset/GPSLocationInfo"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "getLatitude", descriptor: "()I"},
			{name: "getLongitude", descriptor: "()I"},
			{name: "getAltitude", descriptor: "()I"},
			{name: "getHeading", descriptor: "()I"},
			{name: "getHorizontalVelocity", descriptor: "()I"},
			{name: "getVelocityVer", descriptor: "()I"},
			{name: "getAccuracy", descriptor: "()I"},
			{name: "getTimeStamp", descriptor: "()Ljava/lang/String;"},
			{name: "isValid", descriptor: "()I"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/handset/GPSProvider"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "available", descriptor: "()I"},
			{name: "requestLocationInfo", descriptor: "(I)I"},
			{name: "getGPSConfig", descriptor: "()Lorg/kwis/msp/handset/GPSConfig;", access: 0x0008},
			{name: "setGPSConfig", descriptor: "(Lorg/kwis/msp/handset/GPSConfig;)V", access: 0x0008},
			{name: "setLocationInfoListener", descriptor: "(Lorg/kwis/msp/handset/GPSListener;)V", access: 0x0008},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/handset/GPSException"] = ktfHostJavaClassSpec{
		Parent:  "java/lang/Exception",
		methods: ktfThrowableSubclassSpec("java/lang/Exception").methods,
	}
	HostJavaClassSpecs["org/kwis/msp/handset/GPSListener"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		access: 0x0601,
		methods: []ktfHostJavaMethodSpec{
			{name: "LocatinInfoReceived", descriptor: "(Lorg/kwis/msp/handset/GPSLocationInfo;)V", access: 0x0401},
			{name: "LocationInfoReceived", descriptor: "(Lorg/kwis/msp/handset/GPSLocationInfo;)V", access: 0x0401},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/io/IODevice"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/lang/String;I[B)V"},
			{name: "close", descriptor: "()V"},
			{name: "read", descriptor: "([BII)I"},
			{name: "write", descriptor: "([BII)I"},
			{name: "control", descriptor: "(Ljava/lang/String;[B[B)V"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/io/SMSMessage"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "([B)V"},
			// WIPI 2.0 printed Byte[] even though 2.2 corrected it to byte[].
			{name: "<init>", descriptor: "([Ljava/lang/Byte;)V"},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/io/SMS"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "send", descriptor: "(Ljava/lang/String;Lorg/kwis/msp/io/SMSMessage;)I", access: 0x0008},
			{name: "send", descriptor: "(Ljava/lang/String;Ljava/lang/String;Lorg/kwis/msp/io/SMSMessage;)I", access: 0x0008},
			{name: "getMaxMsgLength", descriptor: "()I", access: 0x0008},
			{name: "getMaxMsgLength", descriptor: "(Ljava/lang/String;)I", access: 0x0008},
		},
	}
	HostJavaClassSpecs["org/kwis/msp/io/ResourceGroup"] = ktfHostJavaClassSpec{
		Parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/lang/String;)V"},
			{name: "checkPassword", descriptor: "(Ljava/lang/String;)I"},
			{name: "checkPassword", descriptor: "(Ljava/lang/String;)Z", access: 0x0008},
			{name: "deleteData", descriptor: "(Ljava/lang/String;)I"},
			{name: "deleteData", descriptor: "(Ljava/lang/String;)V"},
			{name: "getCount", descriptor: "()I"},
			{name: "getData", descriptor: "(Ljava/lang/String;)[B"},
			{name: "getFreeSpace", descriptor: "()I", access: 0x0008},
			{name: "getFormat", descriptor: "(Ljava/lang/String;)Ljava/lang/String;"},
			{name: "getID", descriptor: "(Ljava/lang/String;)Ljava/lang/String;"},
			{name: "getList", descriptor: "()[Ljava/lang/String;"},
			{name: "getGroupLockStatus", descriptor: "()I"},
			{name: "getLockStatus", descriptor: "(Ljava/lang/String;)I"},
			{name: "getRegisteredGroup", descriptor: "(Ljava/lang/String;)[Ljava/lang/String;", access: 0x0008},
			{name: "getRegisteredInfo", descriptor: "(Ljava/lang/String;)[Ljava/lang/String;", access: 0x0008},
			{name: "getSize", descriptor: "(Ljava/lang/String;)I"},
			{name: "getSupportedGroups", descriptor: "()[Ljava/lang/String;", access: 0x0008},
			{name: "registerData", descriptor: "(Ljava/lang/String;Ljava/lang/String;)I"},
			{name: "registerData", descriptor: "(Ljava/lang/String;Ljava/lang/String;)V"},
			{name: "setGroupLockStatus", descriptor: "(I)I"},
			{name: "setGroupLockStatus", descriptor: "(I)V"},
			{name: "setLockStatus", descriptor: "(Ljava/lang/String;I)I"},
			{name: "setLockStatus", descriptor: "(Ljava/lang/String;I)V"},
			{name: "writeData", descriptor: "(Ljava/lang/String;Ljava/lang/String;[B)Ljava/lang/String;"},
			{name: "writeData", descriptor: "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;[BZ)Ljava/lang/String;"},
			{name: "exists", descriptor: "(Ljava/lang/String;)Z"},
			{name: "getGroupInfo", descriptor: "(Ljava/lang/String;)Ljava/lang/String;"},
			{name: "getGroupInfo", descriptor: "(Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;"},
			{name: "getGroupInfo", descriptor: "(Ljava/lang/String;)[B"},
			{name: "getInfo", descriptor: "(Ljava/lang/String;Ljava/lang/String;)[B"},
			{name: "search", descriptor: "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Z)[Ljava/lang/String;", access: 0x0008},
			{name: "getUIName", descriptor: "(Ljava/lang/String;)Ljava/lang/String;"},
		},
	}
}

func (r *Runtime) handleWIPI2IODeviceMethod(name, descriptor string) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>(Ljava/lang/String;I[B)V":
		deviceName, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		number, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if deviceName == 0 || number < 0 {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.wipi2IODevices[instance] = &ktfWIPI2IODevice{
			name:   r.javaStringValue(deviceName),
			number: number,
		}
		return 0, nil
	case "close()V":
		if device := r.wipi2IODevices[instance]; device != nil {
			device.closed = true
		}
		return 0, nil
	case "read([BII)I", "write([BII)I":
		device := r.wipi2IODevices[instance]
		if device == nil || device.closed {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		length, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		if name == "write" {
			data, rangeErr := r.readJavaByteArrayRange(array, offset, length)
			if rangeErr != nil {
				return 0, rangeErr
			}
			device.data = append(device.data, data...)
			return uint32(len(data)), nil
		}
		count := min(int(length), len(device.data))
		if count == 0 {
			return 0, nil
		}
		if valueErr := r.writeJavaByteArrayRange(array, offset, device.data[:count]); valueErr != nil {
			return 0, valueErr
		}
		device.data = append(device.data[:0], device.data[count:]...)
		return uint32(count), nil
	case "control(Ljava/lang/String;[B[B)V":
		device := r.wipi2IODevices[instance]
		if device == nil || device.closed {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		command, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		switch strings.ToLower(strings.TrimSpace(r.javaStringValue(command))) {
		case "clear", "reset", "flush":
			device.data = nil
		case "set", "write":
			input, parameterErr := r.parameter(3)
			if parameterErr != nil {
				return 0, parameterErr
			}
			if input != 0 {
				device.data, parameterErr = r.readJavaByteArray(input)
				if parameterErr != nil {
					return 0, parameterErr
				}
			}
		}
		return 0, nil
	}
	return 0, nil
}

func (r *Runtime) handleWIPI2SMSMethod(className, name, descriptor string) (uint32, error) {
	if className == "org/kwis/msp/io/SMSMessage" {
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if name+descriptor == "<init>([B)V" {
			array, valueErr := r.parameter(2)
			if valueErr != nil {
				return 0, valueErr
			}
			if array == 0 {
				return 0, r.raiseHostJavaException("java/lang/NullPointerException")
			}
			data, valueErr := r.readJavaByteArray(array)
			if valueErr != nil {
				return 0, valueErr
			}
			r.wipi2SMSMessages[instance] = data
			return 0, nil
		}
		// The boxed Byte[] spelling only existed in the 2.0 document. Keep the
		// constructor resolvable; an empty payload is safer than interpreting
		// object references as primitive bytes.
		if name+descriptor == "<init>([Ljava/lang/Byte;)V" {
			r.wipi2SMSMessages[instance] = nil
		}
		return 0, nil
	}
	switch name + descriptor {
	case "getMaxMsgLength()I", "getMaxMsgLength(Ljava/lang/String;)I":
		return ktfWIPI2SMSMaxLength, nil
	case "send(Ljava/lang/String;Lorg/kwis/msp/io/SMSMessage;)I":
		number, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		message, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		return r.sendWIPI2SMS(number, message)
	case "send(Ljava/lang/String;Ljava/lang/String;Lorg/kwis/msp/io/SMSMessage;)I":
		number, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		message, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		return r.sendWIPI2SMS(number, message)
	}
	return 0, nil
}

func (r *Runtime) sendWIPI2SMS(number, message uint32) (uint32, error) {
	if number == 0 || message == 0 {
		return 0, r.raiseHostJavaException("java/lang/NullPointerException")
	}
	data, known := r.wipi2SMSMessages[message]
	if !known || len(data) > ktfWIPI2SMSMaxLength {
		return ^uint32(0), nil
	}
	r.tracef("java_sms_send:%s:size=%d", r.javaStringValue(number), len(data))
	return 0, nil
}

func (r *Runtime) handleWIPI2ResourceGroupMethod(name, descriptor string) (uint32, error) {
	signature := name + descriptor
	if signature == "checkPassword(Ljava/lang/String;)Z" {
		password, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if password == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		return 1, nil
	}
	if signature == "getSupportedGroups()[Ljava/lang/String;" {
		return r.newWIPI2StringArray(r.sortedWIPI2ResourceGroupNames(""))
	}
	if signature == "getFreeSpace()I" {
		var used int
		for _, resources := range r.wipi2Resources {
			for _, resource := range resources {
				used += len(resource.data)
			}
		}
		return uint32(max(0, 1024*1024-used)), nil
	}
	if signature == "getRegisteredGroup(Ljava/lang/String;)[Ljava/lang/String;" {
		state, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.newWIPI2StringArray(r.sortedWIPI2ResourceGroupNames(r.javaStringValue(state)))
	}
	if signature == "getRegisteredInfo(Ljava/lang/String;)[Ljava/lang/String;" {
		state, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		// WIPI 2.0 declared this as an instance method, while 2.2 made the
		// identical descriptor static. An old invokevirtual leaves its receiver
		// in the first slot; recognize it and read the state from the next slot.
		if group := r.wipi2ResourceGroups[state]; group != nil {
			state, err = r.parameter(2)
			if err != nil {
				return 0, err
			}
			var names []string
			for name, resource := range r.wipi2Resources[group.name] {
				if r.javaStringValue(state) == "" || resource.state == r.javaStringValue(state) {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			return r.newWIPI2StringArray(names)
		}
		var names []string
		for groupName, resources := range r.wipi2Resources {
			for name, resource := range resources {
				if r.javaStringValue(state) == "" || resource.state == r.javaStringValue(state) {
					names = append(names, groupName+";"+name)
				}
			}
		}
		sort.Strings(names)
		return r.newWIPI2StringArray(names)
	}
	if signature == "search(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Z)[Ljava/lang/String;" {
		return r.searchWIPI2Resources()
	}

	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	if signature == "<init>(Ljava/lang/String;)V" {
		nameAddress, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if nameAddress == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		groupName := r.javaStringValue(nameAddress)
		r.wipi2ResourceGroups[instance] = &ktfWIPI2ResourceGroup{name: groupName}
		if r.wipi2Resources[groupName] == nil {
			r.wipi2Resources[groupName] = make(map[string]*ktfWIPI2Resource)
		}
		return 0, nil
	}
	group := r.wipi2ResourceGroups[instance]
	if group == nil {
		return 0, r.raiseHostJavaException("java/io/IOException")
	}
	resources := r.wipi2Resources[group.name]
	switch signature {
	case "checkPassword(Ljava/lang/String;)I":
		return 0, nil
	case "getCount()I":
		return uint32(len(resources)), nil
	case "getGroupLockStatus()I":
		return uint32(group.lockStatus), nil
	case "setGroupLockStatus(I)I", "setGroupLockStatus(I)V":
		status, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		group.lockStatus = status
		return uint32(status), nil
	case "getList()[Ljava/lang/String;":
		return r.newWIPI2StringArray(sortedWIPI2ResourceNames(resources))
	case "writeData(Ljava/lang/String;Ljava/lang/String;[B)Ljava/lang/String;":
		return r.writeWIPI2Resource(group.name, false)
	case "writeData(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;[BZ)Ljava/lang/String;":
		return r.writeWIPI2Resource(group.name, true)
	}
	nameAddress, err := r.parameter(2)
	if err != nil {
		return 0, err
	}
	resourceName := r.javaStringValue(nameAddress)
	resource := resources[resourceName]
	switch signature {
	case "exists(Ljava/lang/String;)Z":
		if resource != nil {
			return 1, nil
		}
		return 0, nil
	case "deleteData(Ljava/lang/String;)I", "deleteData(Ljava/lang/String;)V":
		if resource == nil {
			return ^uint32(0), nil
		}
		delete(resources, resourceName)
		return 0, nil
	case "getData(Ljava/lang/String;)[B":
		if resource == nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		return r.newJavaByteArray(resource.data)
	case "getSize(Ljava/lang/String;)I":
		if resource == nil {
			return ^uint32(0), nil
		}
		return uint32(len(resource.data)), nil
	case "getFormat(Ljava/lang/String;)Ljava/lang/String;":
		return r.wipi2ResourceString(resource, func(value *ktfWIPI2Resource) string { return value.format })
	case "getID(Ljava/lang/String;)Ljava/lang/String;":
		return r.wipi2ResourceString(resource, func(value *ktfWIPI2Resource) string { return value.id })
	case "getUIName(Ljava/lang/String;)Ljava/lang/String;":
		return r.wipi2ResourceString(resource, func(value *ktfWIPI2Resource) string { return value.uiName })
	case "getLockStatus(Ljava/lang/String;)I":
		if resource == nil {
			return ^uint32(0), nil
		}
		return uint32(resource.lockStatus), nil
	case "setLockStatus(Ljava/lang/String;I)I", "setLockStatus(Ljava/lang/String;I)V":
		if resource == nil {
			return ^uint32(0), nil
		}
		status, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		resource.lockStatus = status
		return uint32(status), nil
	case "registerData(Ljava/lang/String;Ljava/lang/String;)I", "registerData(Ljava/lang/String;Ljava/lang/String;)V":
		if resource == nil {
			return ^uint32(0), nil
		}
		state, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		resource.state = r.javaStringValue(state)
		return 0, nil
	case "getRegisteredInfo(Ljava/lang/String;)[Ljava/lang/String;":
		state := resourceName
		var names []string
		for name, candidate := range resources {
			if state == "" || candidate.state == state {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return r.newWIPI2StringArray(names)
	case "getGroupInfo(Ljava/lang/String;)Ljava/lang/String;":
		return r.NewJavaString(group.name)
	case "getGroupInfo(Ljava/lang/String;)[B":
		return r.newJavaByteArray([]byte(group.name))
	case "getGroupInfo(Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;":
		typeAddress, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.wipi2ResourceInfoString(resource, r.javaStringValue(typeAddress))
	case "getInfo(Ljava/lang/String;Ljava/lang/String;)[B":
		typeAddress, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr := r.wipi2ResourceInfo(resource, r.javaStringValue(typeAddress))
		if valueErr != nil {
			return 0, valueErr
		}
		return r.newJavaByteArray([]byte(value))
	}
	return 0, nil
}

func (r *Runtime) writeWIPI2Resource(groupName string, extended bool) (uint32, error) {
	titleAddress, err := r.parameter(2)
	if err != nil {
		return 0, err
	}
	formatParameter := uint32(3)
	dataParameter := uint32(4)
	uiName := ""
	update := true
	if extended {
		uiNameAddress, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		uiName = r.javaStringValue(uiNameAddress)
		formatParameter = 4
		dataParameter = 5
		updateParameter, valueErr := r.parameter(6)
		if valueErr != nil {
			return 0, valueErr
		}
		update = updateParameter != 0
	}
	formatAddress, err := r.parameter(formatParameter)
	if err != nil {
		return 0, err
	}
	dataAddress, err := r.parameter(dataParameter)
	if err != nil {
		return 0, err
	}
	if titleAddress == 0 || formatAddress == 0 || dataAddress == 0 {
		return 0, r.raiseHostJavaException("java/lang/NullPointerException")
	}
	data, err := r.readJavaByteArray(dataAddress)
	if err != nil {
		return 0, err
	}
	title := r.javaStringValue(titleAddress)
	name := title
	resources := r.wipi2Resources[groupName]
	if existing := resources[name]; existing != nil && !update {
		return 0, r.raiseHostJavaException("java/io/IOException")
	}
	resources[name] = &ktfWIPI2Resource{
		id: name, title: title, uiName: uiName,
		format: r.javaStringValue(formatAddress), data: append([]byte(nil), data...),
	}
	return r.NewJavaString(name)
}

func (r *Runtime) wipi2ResourceString(resource *ktfWIPI2Resource, selectValue func(*ktfWIPI2Resource) string) (uint32, error) {
	if resource == nil {
		return 0, r.raiseHostJavaException("java/io/IOException")
	}
	return r.NewJavaString(selectValue(resource))
}

func (r *Runtime) wipi2ResourceInfoString(resource *ktfWIPI2Resource, infoType string) (uint32, error) {
	value, err := r.wipi2ResourceInfo(resource, infoType)
	if err != nil {
		return 0, err
	}
	return r.NewJavaString(value)
}

func (r *Runtime) wipi2ResourceInfo(resource *ktfWIPI2Resource, infoType string) (string, error) {
	if resource == nil {
		return "", r.raiseHostJavaException("java/io/IOException")
	}
	switch strings.ToLower(infoType) {
	case "id":
		return resource.id, nil
	case "title":
		return resource.title, nil
	case "uiname", "ui_name":
		return resource.uiName, nil
	case "format":
		return resource.format, nil
	case "state":
		return resource.state, nil
	default:
		return "", nil
	}
}

func sortedWIPI2ResourceNames(resources map[string]*ktfWIPI2Resource) []string {
	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Runtime) sortedWIPI2ResourceGroupNames(state string) []string {
	groups := make(map[string]bool)
	for _, group := range r.wipi2ResourceGroups {
		groups[group.name] = true
	}
	for groupName, resources := range r.wipi2Resources {
		for _, resource := range resources {
			if state == "" || resource.state == state {
				groups[groupName] = true
				break
			}
		}
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Runtime) newWIPI2StringArray(values []string) (uint32, error) {
	references := make([]uint32, len(values))
	for index, value := range values {
		stringAddress, err := r.NewJavaString(value)
		if err != nil {
			return 0, err
		}
		references[index] = stringAddress
	}
	return r.newJavaReferenceArray("[Ljava/lang/String;", references)
}

func (r *Runtime) searchWIPI2Resources() (uint32, error) {
	groupAddress, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	typeAddress, err := r.parameter(2)
	if err != nil {
		return 0, err
	}
	queryAddress, err := r.parameter(3)
	if err != nil {
		return 0, err
	}
	exact, err := r.parameter(4)
	if err != nil {
		return 0, err
	}
	groupName := r.javaStringValue(groupAddress)
	infoType := r.javaStringValue(typeAddress)
	query := r.javaStringValue(queryAddress)
	var matches []string
	for name, resource := range r.wipi2Resources[groupName] {
		value, valueErr := r.wipi2ResourceInfo(resource, infoType)
		if valueErr != nil {
			return 0, valueErr
		}
		matched := value == query
		if exact == 0 {
			matched = strings.Contains(value, query)
		}
		if matched {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	return r.newWIPI2StringArray(matches)
}
