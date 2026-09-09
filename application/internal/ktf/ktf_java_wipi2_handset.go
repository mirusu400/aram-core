package ktf

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

const (
	ktfWIPI2AddressFieldName = iota
	ktfWIPI2AddressFieldPhone
	ktfWIPI2AddressFieldEmail
	ktfWIPI2AddressFieldGroup
	ktfWIPI2AddressFieldCount
)

type ktfWIPI2Address struct {
	fields     []uint32
	lockStatus int
}

type ktfWIPI2GPSConfig struct {
	mode, optimization, qos, transport, pdeAddress, pdePort int32
}

type ktfWIPI2GPSLocation struct {
	latitude, longitude, altitude, heading int32
	horizontalVelocity, verticalVelocity   int32
	accuracy                               int32
	timestamp                              string
	valid                                  bool
}

type ktfWIPI2StationLocation struct {
	baseID, latitude, longitude int32
	valid                       bool
}

type ktfWIPI2AddressSnapshot struct {
	Fields     []uint32
	LockStatus int32
}

type ktfWIPI2GPSConfigSnapshot struct {
	Mode, Optimization, QOS, Transport, PDEAddress, PDEPort int32
}

type ktfWIPI2GPSLocationSnapshot struct {
	Latitude, Longitude, Altitude, Heading int32
	HorizontalVelocity, VerticalVelocity   int32
	Accuracy                               int32
	Timestamp                              string
	Valid                                  bool
}

type ktfWIPI2StationLocationSnapshot struct {
	BaseID, Latitude, Longitude int32
	Valid                       bool
}

type ktfWIPI2HandsetSnapshot struct {
	AddressBook      uint32
	AddressBookLock  int32
	AddressGroups    []uint32
	Addresses        map[int32]ktfWIPI2AddressSnapshot
	AddressObjects   map[uint32]int32
	AddressShortcuts map[int32][2]int32
	NextAddressID    int32
	GPSConfigs       map[uint32]ktfWIPI2GPSConfigSnapshot
	GPSConfig        ktfWIPI2GPSConfigSnapshot
	GPSListener      uint32
	GPSLocations     map[uint32]ktfWIPI2GPSLocationSnapshot
	StationLocations map[uint32]ktfWIPI2StationLocationSnapshot
}

func snapshotWIPI2Handset(r *Runtime) ktfWIPI2HandsetSnapshot {
	snapshot := ktfWIPI2HandsetSnapshot{
		AddressBook: r.wipi2AddressBook, AddressBookLock: int32(r.wipi2AddressBookLock),
		AddressGroups:    append([]uint32(nil), r.wipi2AddressGroups...),
		Addresses:        make(map[int32]ktfWIPI2AddressSnapshot, len(r.wipi2Addresses)),
		AddressObjects:   make(map[uint32]int32, len(r.wipi2AddressObjects)),
		AddressShortcuts: make(map[int32][2]int32, len(r.wipi2AddressShortcuts)),
		NextAddressID:    int32(r.wipi2NextAddressID),
		GPSConfigs:       make(map[uint32]ktfWIPI2GPSConfigSnapshot, len(r.wipi2GPSConfigs)),
		GPSConfig:        snapshotWIPI2GPSConfig(r.wipi2GPSConfig),
		GPSListener:      r.wipi2GPSListener,
		GPSLocations:     make(map[uint32]ktfWIPI2GPSLocationSnapshot, len(r.wipi2GPSLocations)),
		StationLocations: make(map[uint32]ktfWIPI2StationLocationSnapshot, len(r.wipi2StationLocations)),
	}
	for recordID, address := range r.wipi2Addresses {
		if address != nil {
			snapshot.Addresses[int32(recordID)] = ktfWIPI2AddressSnapshot{
				Fields:     append([]uint32(nil), address.fields...),
				LockStatus: int32(address.lockStatus),
			}
		}
	}
	for object, recordID := range r.wipi2AddressObjects {
		snapshot.AddressObjects[object] = int32(recordID)
	}
	for shortcut, item := range r.wipi2AddressShortcuts {
		snapshot.AddressShortcuts[int32(shortcut)] = [2]int32{int32(item[0]), int32(item[1])}
	}
	for object, config := range r.wipi2GPSConfigs {
		snapshot.GPSConfigs[object] = snapshotWIPI2GPSConfig(config)
	}
	for object, location := range r.wipi2GPSLocations {
		snapshot.GPSLocations[object] = ktfWIPI2GPSLocationSnapshot{
			Latitude: location.latitude, Longitude: location.longitude,
			Altitude: location.altitude, Heading: location.heading,
			HorizontalVelocity: location.horizontalVelocity,
			VerticalVelocity:   location.verticalVelocity,
			Accuracy:           location.accuracy, Timestamp: location.timestamp,
			Valid: location.valid,
		}
	}
	for object, location := range r.wipi2StationLocations {
		snapshot.StationLocations[object] = ktfWIPI2StationLocationSnapshot{
			BaseID: location.baseID, Latitude: location.latitude,
			Longitude: location.longitude, Valid: location.valid,
		}
	}
	return snapshot
}

func snapshotWIPI2GPSConfig(config ktfWIPI2GPSConfig) ktfWIPI2GPSConfigSnapshot {
	return ktfWIPI2GPSConfigSnapshot{
		Mode: config.mode, Optimization: config.optimization, QOS: config.qos,
		Transport: config.transport, PDEAddress: config.pdeAddress,
		PDEPort: config.pdePort,
	}
}

func restoreWIPI2GPSConfig(config ktfWIPI2GPSConfigSnapshot) ktfWIPI2GPSConfig {
	return ktfWIPI2GPSConfig{
		mode: config.Mode, optimization: config.Optimization, qos: config.QOS,
		transport: config.Transport, pdeAddress: config.PDEAddress,
		pdePort: config.PDEPort,
	}
}

func restoreWIPI2Handset(r *Runtime, snapshot ktfWIPI2HandsetSnapshot) {
	r.wipi2AddressBook = snapshot.AddressBook
	r.wipi2AddressBookLock = int(snapshot.AddressBookLock)
	r.wipi2AddressGroups = append([]uint32(nil), snapshot.AddressGroups...)
	r.wipi2Addresses = make(map[int]*ktfWIPI2Address, len(snapshot.Addresses))
	for recordID, address := range snapshot.Addresses {
		r.wipi2Addresses[int(recordID)] = &ktfWIPI2Address{
			fields:     append([]uint32(nil), address.Fields...),
			lockStatus: int(address.LockStatus),
		}
	}
	r.wipi2AddressObjects = make(map[uint32]int, len(snapshot.AddressObjects))
	for object, recordID := range snapshot.AddressObjects {
		r.wipi2AddressObjects[object] = int(recordID)
	}
	r.wipi2AddressShortcuts = make(map[int][2]int, len(snapshot.AddressShortcuts))
	for shortcut, item := range snapshot.AddressShortcuts {
		r.wipi2AddressShortcuts[int(shortcut)] = [2]int{int(item[0]), int(item[1])}
	}
	r.wipi2NextAddressID = max(1, int(snapshot.NextAddressID))
	r.wipi2GPSConfigs = make(map[uint32]ktfWIPI2GPSConfig, len(snapshot.GPSConfigs))
	for object, config := range snapshot.GPSConfigs {
		r.wipi2GPSConfigs[object] = restoreWIPI2GPSConfig(config)
	}
	r.wipi2GPSConfig = restoreWIPI2GPSConfig(snapshot.GPSConfig)
	r.wipi2GPSListener = snapshot.GPSListener
	r.wipi2GPSLocations = make(map[uint32]ktfWIPI2GPSLocation, len(snapshot.GPSLocations))
	for object, location := range snapshot.GPSLocations {
		r.wipi2GPSLocations[object] = ktfWIPI2GPSLocation{
			latitude: location.Latitude, longitude: location.Longitude,
			altitude: location.Altitude, heading: location.Heading,
			horizontalVelocity: location.HorizontalVelocity,
			verticalVelocity:   location.VerticalVelocity,
			accuracy:           location.Accuracy, timestamp: location.Timestamp,
			valid: location.Valid,
		}
	}
	r.wipi2StationLocations = make(map[uint32]ktfWIPI2StationLocation, len(snapshot.StationLocations))
	for object, location := range snapshot.StationLocations {
		r.wipi2StationLocations[object] = ktfWIPI2StationLocation{
			baseID: location.BaseID, latitude: location.Latitude,
			longitude: location.Longitude, valid: location.Valid,
		}
	}
}

func (r *Runtime) handleWIPI2AddressMethod(className, name, descriptor string) (uint32, error) {
	signature := name + descriptor
	if className == "org/kwis/msp/handset/AddressBook" &&
		signature == "getAddressBook()Lorg/kwis/msp/handset/AddressBook;" {
		if r.wipi2AddressBook == 0 {
			book, err := r.NewHostJavaObject("org/kwis/msp/handset/AddressBook")
			if err != nil {
				return 0, err
			}
			r.wipi2AddressBook = book
		}
		return r.wipi2AddressBook, nil
	}
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	if className == "org/kwis/msp/handset/Address" {
		return r.handleWIPI2AddressRecord(instance, signature)
	}
	switch signature {
	case "getGroupCount()I":
		return uint32(len(r.wipi2AddressGroups)), nil
	case "getGroupName(I)Ljava/lang/String;":
		index, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if index < 0 || index >= len(r.wipi2AddressGroups) {
			return 0, r.raiseHostJavaException("java/lang/IndexOutOfBoundsException")
		}
		return r.wipi2AddressGroups[index], nil
	case "createGroup(Ljava/lang/String;)I":
		group, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if group == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		for index, existing := range r.wipi2AddressGroups {
			if r.javaStringValue(existing) == r.javaStringValue(group) {
				return uint32(index), nil
			}
		}
		r.wipi2AddressGroups = append(r.wipi2AddressGroups, group)
		return uint32(len(r.wipi2AddressGroups) - 1), nil
	case "getFieldCount()I":
		return ktfWIPI2AddressFieldCount, nil
	case "getFieldName(I)Ljava/lang/String;":
		index, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		names := [...]string{"Name", "Phone", "Email", "Group"}
		if index < 0 || index >= len(names) {
			return 0, r.raiseHostJavaException("java/lang/IndexOutOfBoundsException")
		}
		return r.NewJavaString(names[index])
	case "getFieldType(I)I":
		index, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if index < 0 || index >= ktfWIPI2AddressFieldCount {
			return 0, r.raiseHostJavaException("java/lang/IndexOutOfBoundsException")
		}
		if index == ktfWIPI2AddressFieldGroup {
			return 0, nil
		}
		return 1, nil
	case "getFieldMaxLength(I)I":
		index, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		lengths := [...]uint32{64, 32, 128, 4}
		if index < 0 || index >= len(lengths) {
			return 0, r.raiseHostJavaException("java/lang/IndexOutOfBoundsException")
		}
		return lengths[index], nil
	case "getAddressMaxCount()I":
		return 4096, nil
	case "getAddressCount()I":
		return uint32(len(r.wipi2Addresses)), nil
	case "getAddressRecordIdsAll()[I":
		return r.newJavaIntArray(r.sortedWIPI2AddressIDs())
	case "getAddress(I)Lorg/kwis/msp/handset/Address;":
		recordID, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.newWIPI2AddressObject(recordID)
	case "createRecord([Ljava/lang/Object;)I":
		fields, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		values, valueErr := r.readWIPI2ReferenceArray(fields)
		if valueErr != nil {
			return 0, valueErr
		}
		return uint32(r.createWIPI2Address(values)), nil
	case "createRecords([Ljava/lang/Object;)[I":
		records, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		references, valueErr := r.readWIPI2ReferenceArray(records)
		if valueErr != nil {
			return 0, valueErr
		}
		ids := make([]uint32, 0, len(references))
		for _, record := range references {
			fields, fieldErr := r.readWIPI2ReferenceArray(record)
			if fieldErr != nil {
				return 0, fieldErr
			}
			ids = append(ids, uint32(r.createWIPI2Address(fields)))
		}
		return r.newJavaIntArray(ids)
	case "isSupportFieldShortCut()Z":
		return 1, nil
	case "isSupportShortCut(I)Z":
		field, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if field >= 0 && field < ktfWIPI2AddressFieldCount {
			return 1, nil
		}
		return 0, nil
	case "getMaxShortCut()I":
		return 100, nil
	case "getFirstFreeShortCut(I)I":
		start, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for shortcut := max(start, 0); shortcut < 100; shortcut++ {
			if _, used := r.wipi2AddressShortcuts[shortcut]; !used {
				return uint32(shortcut), nil
			}
		}
		return ^uint32(0), nil
	case "setShortCut(III)Z":
		shortcut, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		recordID, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		fieldID, valueErr := r.signedParameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.setWIPI2AddressShortcut(shortcut, recordID, fieldID), nil
	case "setShortCut([I[I[I)Z":
		return r.setWIPI2AddressShortcuts()
	case "getAllShortCut()[I":
		values := make([]uint32, 0, len(r.wipi2AddressShortcuts))
		for shortcut := range r.wipi2AddressShortcuts {
			values = append(values, uint32(shortcut))
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		return r.newJavaIntArray(values)
	case "getShortCutItem(I)[I":
		shortcut, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		item, ok := r.wipi2AddressShortcuts[shortcut]
		if !ok {
			return r.newJavaIntArray([]uint32{^uint32(0), ^uint32(0)})
		}
		return r.newJavaIntArray([]uint32{uint32(item[0]), uint32(item[1])})
	case "getShortCutAssigned(II)I":
		recordID, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		fieldID, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		for shortcut, item := range r.wipi2AddressShortcuts {
			if item == [2]int{recordID, fieldID} {
				return uint32(shortcut), nil
			}
		}
		return ^uint32(0), nil
	case "searchAddress(ILjava/lang/Object;Z)[I":
		return r.searchWIPI2Addresses()
	case "removeAddress(I)Z":
		recordID, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if r.wipi2Addresses[recordID] == nil {
			return 0, nil
		}
		delete(r.wipi2Addresses, recordID)
		for shortcut, item := range r.wipi2AddressShortcuts {
			if item[0] == recordID {
				delete(r.wipi2AddressShortcuts, shortcut)
			}
		}
		return 1, nil
	case "checkPassword(ILjava/lang/String;)I":
		return 0, nil
	case "getLockStatus()I":
		return uint32(r.wipi2AddressBookLock), nil
	case "setLockStatus(I)I":
		status, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.wipi2AddressBookLock = status
		return 0, nil
	}
	return 0, nil
}

func (r *Runtime) handleWIPI2AddressRecord(instance uint32, signature string) (uint32, error) {
	recordID, ok := r.wipi2AddressObjects[instance]
	if !ok {
		return 0, nil
	}
	record := r.wipi2Addresses[recordID]
	if record == nil {
		return 0, nil
	}
	switch signature {
	case "getRecordId()I":
		return uint32(recordID), nil
	case "getField(I)Ljava/lang/Object;":
		index, err := r.signedParameter(2)
		if err != nil {
			return 0, err
		}
		if index < 0 || index >= len(record.fields) {
			return 0, r.raiseHostJavaException("java/lang/IndexOutOfBoundsException")
		}
		return record.fields[index], nil
	case "setField(ILjava/lang/Object;)Z":
		index, err := r.signedParameter(2)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		if index < 0 || index >= ktfWIPI2AddressFieldCount {
			return 0, nil
		}
		for len(record.fields) < ktfWIPI2AddressFieldCount {
			record.fields = append(record.fields, 0)
		}
		record.fields[index] = value
		return 1, nil
	case "getFields()[Ljava/lang/Object;":
		return r.newJavaReferenceArray("[Ljava/lang/Object;", append([]uint32(nil), record.fields...))
	case "setFields([Ljava/lang/Object;)Z":
		fields, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		values, err := r.readWIPI2ReferenceArray(fields)
		if err != nil {
			return 0, err
		}
		record.fields = append([]uint32(nil), values...)
		return 1, nil
	case "getLockStatus()I":
		return uint32(record.lockStatus), nil
	case "setLockStatus(I)I":
		status, err := r.signedParameter(2)
		if err != nil {
			return 0, err
		}
		record.lockStatus = status
		return 0, nil
	}
	return 0, nil
}

func (r *Runtime) createWIPI2Address(fields []uint32) int {
	recordID := r.wipi2NextAddressID
	if recordID < 1 {
		recordID = 1
	}
	r.wipi2NextAddressID = recordID + 1
	r.wipi2Addresses[recordID] = &ktfWIPI2Address{fields: append([]uint32(nil), fields...)}
	return recordID
}

func (r *Runtime) newWIPI2AddressObject(recordID int) (uint32, error) {
	if r.wipi2Addresses[recordID] == nil {
		return 0, nil
	}
	object, err := r.NewHostJavaObject("org/kwis/msp/handset/Address")
	if err != nil {
		return 0, err
	}
	r.wipi2AddressObjects[object] = recordID
	return object, nil
}

func (r *Runtime) sortedWIPI2AddressIDs() []uint32 {
	ids := make([]uint32, 0, len(r.wipi2Addresses))
	for recordID := range r.wipi2Addresses {
		ids = append(ids, uint32(recordID))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (r *Runtime) readWIPI2ReferenceArray(array uint32) ([]uint32, error) {
	if array == 0 {
		return nil, r.raiseHostJavaException("java/lang/NullPointerException")
	}
	fields, err := r.ReadU32(array)
	if err != nil {
		return nil, err
	}
	length, err := r.javaArrayLength(array)
	if err != nil {
		return nil, err
	}
	return r.ReadWords(fields+8, int(length))
}

func (r *Runtime) readWIPI2IntArray(array uint32) ([]int, error) {
	values, err := r.readWIPI2ReferenceArray(array)
	if err != nil {
		return nil, err
	}
	result := make([]int, len(values))
	for index, value := range values {
		result[index] = int(int32(value))
	}
	return result, nil
}

func (r *Runtime) setWIPI2AddressShortcut(shortcut, recordID, fieldID int) uint32 {
	if shortcut < 0 || shortcut >= 100 {
		return 0
	}
	if recordID == -1 {
		delete(r.wipi2AddressShortcuts, shortcut)
		return 1
	}
	if r.wipi2Addresses[recordID] == nil || fieldID < 0 || fieldID >= ktfWIPI2AddressFieldCount {
		return 0
	}
	if _, used := r.wipi2AddressShortcuts[shortcut]; used {
		return 0
	}
	r.wipi2AddressShortcuts[shortcut] = [2]int{recordID, fieldID}
	return 1
}

func (r *Runtime) setWIPI2AddressShortcuts() (uint32, error) {
	shortcutArray, err := r.parameter(2)
	if err != nil {
		return 0, err
	}
	recordArray, err := r.parameter(3)
	if err != nil {
		return 0, err
	}
	fieldArray, err := r.parameter(4)
	if err != nil {
		return 0, err
	}
	shortcuts, err := r.readWIPI2IntArray(shortcutArray)
	if err != nil {
		return 0, err
	}
	records, err := r.readWIPI2IntArray(recordArray)
	if err != nil {
		return 0, err
	}
	fields, err := r.readWIPI2IntArray(fieldArray)
	if err != nil {
		return 0, err
	}
	if len(shortcuts) != len(records) || len(records) != len(fields) {
		return 0, nil
	}
	copyOfShortcuts := make(map[int][2]int, len(r.wipi2AddressShortcuts))
	for key, value := range r.wipi2AddressShortcuts {
		copyOfShortcuts[key] = value
	}
	for index := range shortcuts {
		if r.setWIPI2AddressShortcut(shortcuts[index], records[index], fields[index]) == 0 {
			r.wipi2AddressShortcuts = copyOfShortcuts
			return 0, nil
		}
	}
	return 1, nil
}

func (r *Runtime) searchWIPI2Addresses() (uint32, error) {
	searchBy, err := r.signedParameter(2)
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
	fieldIndex := searchBy
	if fieldIndex < 0 || fieldIndex >= ktfWIPI2AddressFieldCount {
		return r.newJavaIntArray(nil)
	}
	query := r.javaObjectComparableText(queryAddress)
	var matches []uint32
	for recordID, record := range r.wipi2Addresses {
		if fieldIndex >= len(record.fields) {
			continue
		}
		value := r.javaObjectComparableText(record.fields[fieldIndex])
		matched := value == query
		if exact == 0 {
			matched = strings.Contains(strings.ToLower(value), strings.ToLower(query))
		}
		if matched {
			matches = append(matches, uint32(recordID))
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i] < matches[j] })
	return r.newJavaIntArray(matches)
}

func (r *Runtime) javaObjectComparableText(object uint32) string {
	if value, ok := r.JavaStrings[object]; ok {
		return value
	}
	if value, ok := r.integerValues[object]; ok {
		return strconv.FormatInt(int64(value), 10)
	}
	return r.javaObjectString(object)
}

func (r *Runtime) handleWIPI2LocationMethod(ctx context.Context, className, name, descriptor string) (uint32, error) {
	signature := name + descriptor
	switch className {
	case "org/kwis/msp/handset/StationLocationInfo":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		location := r.wipi2StationLocations[instance]
		switch signature {
		case "<init>()V":
			r.wipi2StationLocations[instance] = location
			return 0, nil
		case "getLocationInfo()I":
			location.valid = true
			r.wipi2StationLocations[instance] = location
			return 0, nil
		case "getBaseID()I":
			return uint32(location.baseID), nil
		case "getBaseLat()I":
			return uint32(location.latitude), nil
		case "getBaseLong()I":
			return uint32(location.longitude), nil
		case "isValid()Z":
			if location.valid {
				return 1, nil
			}
			return 0, nil
		}
	case "org/kwis/msp/handset/GPSConfig":
		return r.handleWIPI2GPSConfigMethod(signature)
	case "org/kwis/msp/handset/GPSLocationInfo":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		location := r.wipi2GPSLocations[instance]
		switch signature {
		case "<init>()V":
			r.wipi2GPSLocations[instance] = location
			return 0, nil
		case "getLatitude()I":
			return uint32(location.latitude), nil
		case "getLongitude()I":
			return uint32(location.longitude), nil
		case "getAltitude()I":
			return uint32(location.altitude), nil
		case "getHeading()I":
			return uint32(location.heading), nil
		case "getHorizontalVelocity()I":
			return uint32(location.horizontalVelocity), nil
		case "getVelocityVer()I":
			return uint32(location.verticalVelocity), nil
		case "getAccuracy()I":
			return uint32(location.accuracy), nil
		case "getTimeStamp()Ljava/lang/String;":
			return r.NewJavaString(location.timestamp)
		case "isValid()I":
			if location.valid {
				return 1, nil
			}
			return 0, nil
		}
	case "org/kwis/msp/handset/GPSProvider":
		return r.handleWIPI2GPSProviderMethod(ctx, signature)
	}
	return 0, nil
}

func (r *Runtime) handleWIPI2GPSConfigMethod(signature string) (uint32, error) {
	if strings.HasPrefix(signature, "set") {
		value, err := r.signedParameter(1)
		if err != nil {
			return 0, err
		}
		switch signature {
		case "setMode(I)I":
			r.wipi2GPSConfig.mode = int32(value)
		case "setOptimization(I)I":
			r.wipi2GPSConfig.optimization = int32(value)
		case "setQos(I)I":
			r.wipi2GPSConfig.qos = int32(value)
		case "setTransport(I)I":
			r.wipi2GPSConfig.transport = int32(value)
		case "setPdeAddr(I)I":
			r.wipi2GPSConfig.pdeAddress = int32(value)
		case "setPdePort(I)I":
			if value < 0 || value > 65535 {
				return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
			}
			r.wipi2GPSConfig.pdePort = int32(value)
		}
		return 0, nil
	}
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	if signature == "<init>(IIIIII)V" {
		values := make([]int32, 6)
		for index := range values {
			value, valueErr := r.signedParameter(uint32(index + 2))
			if valueErr != nil {
				return 0, valueErr
			}
			values[index] = int32(value)
		}
		if values[5] < 0 || values[5] > 65535 {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		config := ktfWIPI2GPSConfig{
			mode: values[0], optimization: values[1], qos: values[2],
			transport: values[3], pdeAddress: values[4], pdePort: values[5],
		}
		r.wipi2GPSConfigs[instance] = config
		return 0, nil
	}
	config := r.wipi2GPSConfigs[instance]
	switch signature {
	case "getMode()I":
		return uint32(config.mode), nil
	case "getOptimization()I":
		return uint32(config.optimization), nil
	case "getQos()I":
		return uint32(config.qos), nil
	case "getTransport()I":
		return uint32(config.transport), nil
	case "getPdeAddr()I":
		return uint32(config.pdeAddress), nil
	case "getPdePort()I":
		return uint32(config.pdePort), nil
	}
	return 0, nil
}

func (r *Runtime) handleWIPI2GPSProviderMethod(ctx context.Context, signature string) (uint32, error) {
	switch signature {
	case "<init>()V", "available()I":
		return 0, nil
	case "getGPSConfig()Lorg/kwis/msp/handset/GPSConfig;":
		config, err := r.NewHostJavaObject("org/kwis/msp/handset/GPSConfig")
		if err != nil {
			return 0, err
		}
		r.wipi2GPSConfigs[config] = r.wipi2GPSConfig
		return config, nil
	case "setGPSConfig(Lorg/kwis/msp/handset/GPSConfig;)V":
		config, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if config == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		r.wipi2GPSConfig = r.wipi2GPSConfigs[config]
		return 0, nil
	case "setLocationInfoListener(Lorg/kwis/msp/handset/GPSListener;)V":
		listener, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		r.wipi2GPSListener = listener
		return 0, nil
	case "requestLocationInfo(I)I":
		repeat, err := r.signedParameter(2)
		if err != nil {
			return 0, err
		}
		if repeat < -1 {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		if repeat == -1 || r.wipi2GPSListener == 0 {
			return 0, nil
		}
		location, err := r.NewHostJavaObject("org/kwis/msp/handset/GPSLocationInfo")
		if err != nil {
			return 0, err
		}
		r.wipi2GPSLocations[location] = ktfWIPI2GPSLocation{
			timestamp: strconv.FormatUint(r.monotonicReadMS(), 10),
			valid:     true,
		}
		if err := r.QueueJavaVirtual(
			r.wipi2GPSListener,
			"LocatinInfoReceived",
			"(Lorg/kwis/msp/handset/GPSLocationInfo;)V",
			location,
		); err != nil {
			return 0, err
		}
		r.tracef("java_gps_location_requested:repeat=%d", repeat)
		_ = ctx
		return 0, nil
	}
	return 0, nil
}
