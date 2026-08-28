package ktf

import (
	"context"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func ktfGetWIPICInterface(_ context.Context, runtime *Runtime) (uint32, error) {
	return runtime.buildWIPICInterface()
}

func (r *Runtime) buildWIPICInterface() (uint32, error) {
	if r.wipicInterface != 0 {
		return r.wipicInterface, nil
	}
	const (
		// The kernel reserved1 getter returns the 17-entry master vector, not
		// the 18-entry named registration array. Its order is util, misc,
		// graphic, im, db, plugin, fs, serial, uic, media, net, phn, ann,
		// ioDev, termRes, math, ssl.
		interfaceCount = 17
		slotsPerTable  = 64
	)
	interfaces := make([]uint32, interfaceCount)
	for table := range interfaces {
		slots := make([]uint32, slotsPerTable)
		for index := range slots {
			slots[index] = r.RegisterHostCall(
				fmt.Sprintf("wipic.%d.%d", table, index),
				ktfWIPICHandler(table, index),
			)
		}
		address, err := r.AllocateWords(slotsPerTable)
		if err != nil {
			return 0, err
		}
		if err := r.writeWords(address, slots); err != nil {
			return 0, err
		}
		interfaces[table] = address
	}
	address, err := r.AllocateWords(interfaceCount)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(address, interfaces); err != nil {
		return 0, err
	}
	r.wipicInterface = address
	return address, nil
}

const (
	ktfWIPICMasterGraphics = 2
	ktfWIPICMasterFS       = 6
	ktfWIPICMasterMedia    = 9
	ktfWIPICMasterNet      = 10
)

func ktfWIPICHandler(table, slot int) ktfHostHandler {
	if table == ktfWIPICMasterGraphics {
		switch slot {
		case 0:
			return ktfWIPICGraphicsGetImageProperty
		case 1:
			return ktfWIPICGraphicsGetImageFramebuffer
		case 2:
			return ktfWIPICGraphicsGetScreenFramebuffer
		case 3:
			return ktfWIPICGraphicsDestroyOffscreenFramebuffer
		case 4:
			return ktfWIPICGraphicsCreateOffscreenFramebuffer
		case 5:
			return ktfWIPICGraphicsInitContext
		case 6:
			return ktfWIPICGraphicsSetContext
		case 7:
			return ktfWIPICGraphicsGetContext
		case 8:
			return ktfWIPICGraphicsPutPixel
		case 9:
			return ktfWIPICGraphicsDrawLine
		case 10:
			return ktfWIPICGraphicsDrawRect
		case 11:
			return ktfWIPICGraphicsFillRect
		case 12:
			return ktfWIPICGraphicsCopyFramebuffer
		case 13:
			return ktfWIPICGraphicsDrawImage
		case 14:
			return ktfWIPICGraphicsCopyArea
		case 15:
			return ktfWIPICGraphicsDrawArc
		case 16:
			return ktfWIPICGraphicsFillArc
		case 17:
			return ktfWIPICGraphicsDrawString
		case 18:
			return ktfWIPICGraphicsDrawUnicodeString
		case 19:
			return ktfWIPICGraphicsGetRGBPixels
		case 20:
			return ktfWIPICGraphicsSetRGBPixels
		case 21:
			return ktfWIPICGraphicsFlushLCD
		case 22:
			return ktfWIPICGraphicsGetPixelFromRGB
		case 23:
			return ktfWIPICGraphicsGetRGBFromPixel
		case 24:
			return ktfWIPICGraphicsGetDisplayInfo
		case 25:
			return ktfWIPICGraphicsRepaint
		case 26:
			return ktfWIPICGraphicsGetFont
		case 27:
			return ktfWIPICGraphicsGetFontHeight
		case 28:
			return ktfWIPICGraphicsGetFontAscent
		case 29:
			return ktfWIPICGraphicsGetFontDescent
		case 30:
			return ktfWIPICGraphicsGetStringWidth
		case 31:
			return ktfWIPICGraphicsGetUnicodeStringWidth
		case 32:
			return ktfWIPICGraphicsCreateImage
		case 33:
			return ktfWIPICGraphicsDestroyImage
		case 34:
			return ktfWIPICGraphicsDecodeNextImage
		case 35:
			return ktfWIPICGraphicsEncodeImage
		case 36:
			return ktfWIPICGraphicsPostEvent
		case 37:
			return ktfWIPICGraphicsDrawPolygon
		case 38:
			return ktfWIPICGraphicsDrawFillPolygon
		}
	}
	if table == ktfWIPICMasterFS {
		switch slot {
		case 0:
			return ktfWIPICFileOpen
		case 1:
			return ktfWIPICFileRead
		case 2:
			return ktfWIPICFileWrite
		case 3:
			return ktfWIPICFileClose
		case 4:
			return ktfWIPICFileSeek
		case 5:
			return ktfWIPICFileAttribute
		case 6:
			return ktfWIPICFileRemove
		case 7:
			return ktfWIPICFileRename
		case 8:
			return ktfWIPICFileMakeDirectory
		case 9:
			return ktfWIPICFileRemoveDirectory
		case 10:
			return ktfWIPICFileList
		case 11:
			return ktfWIPICFileTotalSpace
		case 12:
			return ktfWIPICFileAvailable
		case 13:
			return ktfWIPICFileSetMode
		case 14:
			return ktfWIPICFileGetCounts
		case 15:
			return ktfWIPICFileTell
		case 16:
			return ktfWIPICFileIsExist
		}
	}
	if table == ktfWIPICMasterNet {
		switch slot {
		case 0:
			return ktfWIPICNetConnect
		case 1:
			return ktfWIPICNetClose
		}
	}
	if table == ktfWIPICMasterMedia {
		switch slot {
		case 0:
			return ktfWIPICMediaCreate
		case 3:
			return ktfWIPICMediaDestroy
		case 4:
			return ktfWIPICMediaPutData
		case 5:
			return ktfWIPICMediaGetData
		case 6:
			return ktfWIPICMediaAvailableDataSize
		case 7:
			return ktfWIPICMediaClearData
		case 8:
			return ktfWIPICMediaPlay
		case 9:
			return ktfWIPICMediaPause
		case 10:
			return ktfWIPICMediaResume
		case 11:
			return ktfWIPICMediaStop
		case 13:
			return ktfWIPICMediaSetPosition
		case 16:
			return ktfWIPICMediaGetState
		}
	}
	return ktfWIPICNoop(table, slot)
}

func ktfWIPICMediaCreate(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	mediaTypeAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	capacity, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	callback, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if mediaTypeAddress == 0 || capacity == 0 ||
		uint64(capacity) > runtime.serviceConfig.Limits.Media.MaxSourceBytes {
		return 0, nil
	}
	mediaType, err := runtime.readCString(mediaTypeAddress, 256)
	if err != nil {
		return 0, err
	}
	serviceMediaType := ""
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	// The MA-2/MA-3/MA-5 identifiers name Yamaha synth generations; their
	// clip payloads are all SMAF containers (마스터오브소드3 creates its one
	// clip as Yamaha_MA2 and would otherwise run silent).
	case "yamaha_ma2", "yamaha_ma3", "yamaha_ma5",
		"audio/x-smaf", "audio/smaf", "audio/mmf":
		serviceMediaType = "audio/x-smaf"
	case "audio/midi", "audio/sp-midi":
		serviceMediaType = "audio/midi"
	case "audio/wav", "audio/x-wav":
		serviceMediaType = "audio/wav"
	default:
		runtime.tracef("wipic_media_create_unsupported:type=%q", mediaType)
		return 0, nil
	}
	handle, err := runtime.AllocateWords(24)
	if err != nil {
		return 0, err
	}
	serviceID, err := runtime.Services.Media.CreateClip(
		runtime.ServiceOwner,
		serviceMediaType,
		uint64(capacity),
	)
	if err != nil {
		runtime.Heap.Release(handle)
		return 0, err
	}
	runtime.wipicMediaClips[handle] = &ktfWIPICMediaClip{
		mediaType: serviceMediaType,
		capacity:  capacity,
		callback:  callback,
		volume:    100,
	}
	runtime.wipicMediaServices[handle] = serviceID
	runtime.tracef(
		"wipic_media_create:handle=0x%08x:type=%s:capacity=%d:"+
			"callback=0x%08x",
		handle,
		mediaType,
		capacity,
		callback,
	)
	return handle, nil
}

func ktfWIPICMediaDestroy(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.Services.Media.DestroyClip(
		runtime.ServiceOwner,
		serviceID,
		runtime.Services.Events,
	); err != nil {
		return 0, err
	}
	delete(runtime.wipicMediaClips, handle)
	delete(runtime.wipicMediaServices, handle)
	runtime.Heap.Release(handle)
	return 0, nil
}

func ktfWIPICMediaPutData(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	input, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if input == 0 || count > clip.capacity ||
		uint64(len(clip.data))+uint64(count) > uint64(clip.capacity) {
		return ktfWIPICErrorInvalid, nil
	}
	data := make([]byte, count)
	if err := runtime.CPU.ReadMemory(input, data); err != nil {
		return 0, err
	}
	if _, err := runtime.Services.Media.Append(
		runtime.ServiceOwner,
		serviceID,
		data,
	); err != nil {
		return 0, err
	}
	clip.data = append(clip.data, data...)
	runtime.tracef("wipic_media_putdata:handle=0x%08x:add=%d:total=%d", handle, count, len(clip.data))
	// MC_mdaClipPutData answers with the number of bytes it accepted, not a
	// zero status. 질주쾌감 스케쳐 loads every clip through
	// `if (MC_mdaClipPutData(...) <= 0) { MC_mdaClipFree(clip); return; }`,
	// so returning zero read as a failed write and the title freed each clip
	// instead of ever reaching MC_mdaPlay — it ran completely silent.
	return count, nil
}

func ktfWIPICMediaGetData(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	count = min(count, uint32(len(clip.data)))
	if output == 0 && count != 0 {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.CPU.WriteMemory(output, clip.data[:count]); err != nil {
		return 0, err
	}
	clip.data = append(clip.data[:0], clip.data[count:]...)
	if err := runtime.Services.Media.Clear(
		runtime.ServiceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	if _, err := runtime.Services.Media.Append(
		runtime.ServiceOwner,
		serviceID,
		clip.data,
	); err != nil {
		return 0, err
	}
	return count, nil
}

func ktfWIPICMediaAvailableDataSize(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, clip, _, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	return uint32(len(clip.data)), nil
}

func ktfWIPICMediaClearData(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.Services.Media.Clear(
		runtime.ServiceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	runtime.tracef("wipic_media_clear:handle=0x%08x:had=%d", handle, len(clip.data))
	clip.data = nil
	clip.state = 0
	clip.repeat = false
	return 0, nil
}

func ktfWIPICMediaPlay(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil || len(clip.data) == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	repeat, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	plays := int32(1)
	if repeat != 0 {
		plays = -1
	}
	if err := runtime.Services.Media.Play(
		runtime.ServiceOwner,
		serviceID,
		plays,
	); err != nil {
		return 0, err
	}
	clip.state = 1
	clip.repeat = repeat != 0
	runtime.tracef(
		"wipic_media_play:handle=0x%08x:size=%d:repeat=%t",
		handle,
		len(clip.data),
		clip.repeat,
	)
	return 0, nil
}

func ktfWIPICMediaPause(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.Services.Media.Pause(
		runtime.ServiceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	runtime.tracef("wipic_media_pause:handle=0x%08x", handle)
	clip.state = 2
	return 0, nil
}

func ktfWIPICMediaResume(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.Services.Media.Resume(
		runtime.ServiceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	runtime.tracef("wipic_media_resume:handle=0x%08x", handle)
	clip.state = 1
	return 0, nil
}

func ktfWIPICMediaStop(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.Services.Media.Stop(
		runtime.ServiceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	runtime.tracef("wipic_media_stop:handle=0x%08x:repeat=%t", handle, clip.repeat)
	clip.state = 0
	clip.repeat = false
	return 0, nil
}

// ktfWIPICMediaGetState reports the clip's playback state (0 stopped,
// 1 playing, 2 paused). 드래곤로드 shares one clip handle between its BGM and
// effect sounds and polls this slot to learn when an effect finished so it
// can reload the background track (issue #48).
func ktfWIPICMediaGetState(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, clip, _, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if !clip.tracedState || clip.state != clip.lastTracedState {
		runtime.tracef(
			"wipic_media_state:handle=0x%08x:state=%d",
			handle,
			clip.state,
		)
		clip.lastTracedState = clip.state
		clip.tracedState = true
	}
	return uint32(clip.state), nil
}

func ktfWIPICMediaSetPosition(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	position, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if err := runtime.Services.Media.Seek(
		runtime.ServiceOwner,
		serviceID,
		time.Duration(position)*time.Millisecond,
	); err != nil {
		return ktfWIPICErrorInvalid, nil
	}
	return 0, nil
}

func (r *Runtime) ktfWIPICMediaParameter() (
	uint32,
	*ktfWIPICMediaClip,
	shared.ServiceID,
	error,
) {
	handle, err := r.parameter(0)
	if err != nil {
		return 0, nil, 0, err
	}
	return handle, r.wipicMediaClips[handle], r.wipicMediaServices[handle], nil
}
