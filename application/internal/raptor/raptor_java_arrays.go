package raptor

// A Raptor Java array exists twice: as the body the AOT code reads and writes
// with plain loads and stores (obj+8 -> body, body+0 = length, elements from
// body+4), and as a KTF mirror the shared Java host operates on (instance ->
// [body, class], body+4 = length, elements from body+8).
//
// Neither side sees the other's writes. Element stores the AOT compiles inline
// never reach the mirror, and a host method that fills an array argument -
// InputStream.read(byte[]) above all - only ever fills the mirror. 현영맞고2006
// reads each of its seven sprite atlases with
//
//	InputStream in = getClass().getResourceAsStream("/img/imenu.dat");
//	byte[] data = new byte[in.available()];
//	in.read(data);
//
// and then parses the sprite count out of data. Every byte the parse read was
// zero, so every atlas produced a zero-length Image[] and every screen after
// the opening notice drew nothing but its background (issue #79).
//
// A host call is the only point where the two copies have to agree, so the
// bridge copies the elements across it: guest body to mirror before the call
// so the host reads what the AOT wrote, and mirror back to guest body after it
// so the AOT reads what the host wrote. Only primitive arrays are copied;
// reference elements are heap addresses that mean different objects on the two
// sides, and the bridge already mirrors those one store at a time.

// noteRaptorPrimitiveArray records that a mirror handle names a primitive
// array whose elements the bridge has to copy, and how wide one element is.
// Host calls take far more non-array arguments than array ones, so the sync
// asks this table first rather than inspecting every argument's class.
func (r *Runtime) noteRaptorPrimitiveArray(
	java *JavaRuntime,
	mirror uint32,
	element uint32,
) {
	if mirror == 0 || element == 0 {
		return
	}
	if java.primitiveArrays == nil {
		java.primitiveArrays = make(map[uint32]uint32)
	}
	java.primitiveArrays[mirror] = element
}

// syncRaptorArrayArguments copies the elements of every mapped primitive array
// among arguments between its guest body and its KTF mirror. Arguments hold
// mirror handles by the time this runs, so the guest instance comes back
// through ktfToLGT.
func (r *Runtime) syncRaptorArrayArguments(
	java *JavaRuntime,
	arguments []uint32,
	toMirror bool,
) {
	if len(java.primitiveArrays) == 0 {
		return
	}
	for _, mirror := range arguments {
		element := java.primitiveArrays[mirror]
		if element == 0 {
			continue
		}
		if instance := java.ktfToLGT[mirror]; instance != 0 {
			r.syncRaptorArray(java, instance, mirror, element, toMirror)
		}
	}
}

// syncRaptorArray copies one primitive array's elements in the requested
// direction. A mismatch on either side is not an error: an array body that has
// gone missing, or two lengths that disagree, copies what both can hold.
func (r *Runtime) syncRaptorArray(
	java *JavaRuntime,
	instance uint32,
	mirror uint32,
	element uint32,
	toMirror bool,
) {
	mirrorWords, err := java.Host.ReadWords(mirror, 2)
	if err != nil || mirrorWords[0] == 0 {
		return
	}
	mirrorCount, err := r.Public.ReadU32(mirrorWords[0] + 4)
	if err != nil {
		return
	}
	body, err := r.Public.ReadU32(instance + 8)
	if err != nil || body == 0 {
		return
	}
	count, err := r.Public.ReadU32(body)
	if err != nil {
		return
	}
	if count > mirrorCount {
		count = mirrorCount
	}
	if count == 0 || count > maxRaptorArraySyncElements {
		return
	}
	size := count * element
	if uint32(cap(java.syncScratch)) < size {
		java.syncScratch = make([]byte, size)
	}
	buffer := java.syncScratch[:size]
	source, destination := mirrorWords[0]+8, body+4
	if toMirror {
		source, destination = body+4, mirrorWords[0]+8
	}
	if err := r.CPU.ReadMemory(source, buffer); err != nil {
		return
	}
	_ = r.CPU.WriteMemory(destination, buffer)
}

// maxRaptorArraySyncElements bounds one copy so a corrupt length word cannot
// make the bridge allocate or move an absurd buffer. The largest array a title
// hands a host method is a resource read buffer, well under this.
const maxRaptorArraySyncElements = 1 << 24
