//go:build cgo && (darwin || freebsd || linux || windows)

package unicornbackend

/*
#cgo windows LDFLAGS: -lkernel32
#cgo freebsd linux LDFLAGS: -ldl

#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#else
#include <dlfcn.h>
#endif

typedef uint32_t (*aram_uc_version_fn)(uint32_t *, uint32_t *);
typedef int32_t (*aram_uc_open_fn)(int32_t, int32_t, void **);
typedef int32_t (*aram_uc_close_fn)(void *);
typedef int32_t (*aram_uc_reg_read_fn)(void *, int32_t, void *);
typedef int32_t (*aram_uc_reg_write_fn)(void *, int32_t, const void *);
typedef int32_t (*aram_uc_mem_read_fn)(void *, uint64_t, void *, uint64_t);
typedef int32_t (*aram_uc_mem_write_fn)(void *, uint64_t, const void *, uint64_t);
typedef int32_t (*aram_uc_mem_map_fn)(void *, uint64_t, uint64_t, uint32_t);
typedef int32_t (*aram_uc_emu_start_fn)(void *, uint64_t, uint64_t, uint64_t, size_t);
typedef int32_t (*aram_uc_emu_stop_fn)(void *);

typedef enum aram_unicorn_operation {
	ARAM_UC_VERSION,
	ARAM_UC_OPEN,
	ARAM_UC_CLOSE,
	ARAM_UC_REG_READ,
	ARAM_UC_REG_WRITE,
	ARAM_UC_MEM_READ,
	ARAM_UC_MEM_WRITE,
	ARAM_UC_MEM_MAP,
	ARAM_UC_EMU_START,
	ARAM_UC_EMU_STOP
} aram_unicorn_operation;

typedef struct aram_unicorn_call {
	aram_unicorn_operation operation;
	int32_t result;
	int32_t architecture;
	int32_t mode;
	int32_t register_id;
	uintptr_t engine;
	uintptr_t opened_engine;
	uint64_t address;
	uint64_t size;
	uint64_t begin;
	uint64_t until;
	uint64_t timeout;
	size_t count;
	uint32_t permissions;
	uint32_t version;
	uint32_t major;
	uint32_t minor;
	void *data;
} aram_unicorn_call;

typedef struct aram_unicorn_api {
	void *library;
	aram_uc_version_fn version;
	aram_uc_open_fn open;
	aram_uc_close_fn close;
	aram_uc_reg_read_fn reg_read;
	aram_uc_reg_write_fn reg_write;
	aram_uc_mem_read_fn mem_read;
	aram_uc_mem_write_fn mem_write;
	aram_uc_mem_map_fn mem_map;
	aram_uc_emu_start_fn emu_start;
	aram_uc_emu_stop_fn emu_stop;
#ifdef _WIN32
	HANDLE worker_thread;
	HANDLE request_event;
	HANDLE response_event;
	CRITICAL_SECTION worker_lock;
	aram_unicorn_call *pending_call;
	volatile LONG worker_stopping;
#endif
} aram_unicorn_api;

static void aram_execute_call(aram_unicorn_api *api, aram_unicorn_call *call) {
	void *engine = (void *)call->engine;
	switch (call->operation) {
	case ARAM_UC_VERSION:
		call->version = api->version(&call->major, &call->minor);
		break;
	case ARAM_UC_OPEN:
		engine = NULL;
		call->result = api->open(call->architecture, call->mode, &engine);
		if (call->result == 0) {
			call->opened_engine = (uintptr_t)engine;
		}
		break;
	case ARAM_UC_CLOSE:
		call->result = api->close(engine);
		break;
	case ARAM_UC_REG_READ:
		call->result = api->reg_read(engine, call->register_id, call->data);
		break;
	case ARAM_UC_REG_WRITE:
		call->result = api->reg_write(engine, call->register_id, call->data);
		break;
	case ARAM_UC_MEM_READ:
		call->result = api->mem_read(engine, call->address, call->data, call->size);
		break;
	case ARAM_UC_MEM_WRITE:
		call->result = api->mem_write(engine, call->address, call->data, call->size);
		break;
	case ARAM_UC_MEM_MAP:
		call->result = api->mem_map(
			engine, call->address, call->size, call->permissions);
		break;
	case ARAM_UC_EMU_START:
		call->result = api->emu_start(
			engine, call->begin, call->until, call->timeout, call->count);
		break;
	case ARAM_UC_EMU_STOP:
		call->result = api->emu_stop(engine);
		break;
	}
}

#ifdef _WIN32
// Unicorn 2.1 commits its translation buffer by handling deliberate access
// violations. Go's Windows exception handler claims those faults on a Go-owned
// thread before Unicorn can service them. Dispatching native calls through one
// ordinary Windows worker thread preserves Unicorn's documented demand-paging
// behavior and also keeps every call for an engine on one native thread.
static DWORD WINAPI aram_unicorn_worker(void *parameter) {
	aram_unicorn_api *api = (aram_unicorn_api *)parameter;
	for (;;) {
		if (WaitForSingleObject(api->request_event, INFINITE) != WAIT_OBJECT_0) {
			return 1;
		}
		if (InterlockedCompareExchange(&api->worker_stopping, 0, 0) != 0) {
			return 0;
		}
		aram_execute_call(api, api->pending_call);
		SetEvent(api->response_event);
	}
}

static int aram_worker_start(
	aram_unicorn_api *api,
	char *error,
	size_t error_size
) {
	InitializeCriticalSection(&api->worker_lock);
	api->request_event = CreateEventA(NULL, FALSE, FALSE, NULL);
	if (api->request_event != NULL) {
		api->response_event = CreateEventA(NULL, FALSE, FALSE, NULL);
	}
	if (api->response_event != NULL) {
		api->worker_thread = CreateThread(
			NULL, 0, aram_unicorn_worker, api, 0, NULL);
	}
	if (api->worker_thread != NULL) {
		return 1;
	}
	DWORD code = GetLastError();
	if (api->response_event != NULL) {
		CloseHandle(api->response_event);
	}
	if (api->request_event != NULL) {
		CloseHandle(api->request_event);
	}
	DeleteCriticalSection(&api->worker_lock);
	api->response_event = NULL;
	api->request_event = NULL;
	snprintf(error, error_size, "create native Unicorn worker: Windows error %lu",
		(unsigned long)code);
	return 0;
}

static void aram_worker_stop(aram_unicorn_api *api) {
	if (api->worker_thread == NULL) {
		return;
	}
	EnterCriticalSection(&api->worker_lock);
	InterlockedExchange(&api->worker_stopping, 1);
	SetEvent(api->request_event);
	LeaveCriticalSection(&api->worker_lock);
	WaitForSingleObject(api->worker_thread, INFINITE);
	CloseHandle(api->worker_thread);
	CloseHandle(api->response_event);
	CloseHandle(api->request_event);
	DeleteCriticalSection(&api->worker_lock);
	api->worker_thread = NULL;
	api->response_event = NULL;
	api->request_event = NULL;
}

static void aram_dispatch_call(aram_unicorn_api *api, aram_unicorn_call *call) {
	EnterCriticalSection(&api->worker_lock);
	api->pending_call = call;
	SetEvent(api->request_event);
	WaitForSingleObject(api->response_event, INFINITE);
	api->pending_call = NULL;
	LeaveCriticalSection(&api->worker_lock);
}
#else
static int aram_worker_start(
	aram_unicorn_api *api,
	char *error,
	size_t error_size
) {
	(void)api;
	(void)error;
	(void)error_size;
	return 1;
}

static void aram_worker_stop(aram_unicorn_api *api) {
	(void)api;
}

static void aram_dispatch_call(aram_unicorn_api *api, aram_unicorn_call *call) {
	aram_execute_call(api, call);
}
#endif

static void aram_set_error(char *destination, size_t size, const char *message) {
	if (destination == NULL || size == 0) {
		return;
	}
	if (message == NULL || message[0] == '\0') {
		message = "native dynamic-loader error";
	}
	snprintf(destination, size, "%s", message);
}

static void *aram_library_open(const char *path, char *error, size_t error_size) {
#ifdef _WIN32
	HMODULE library = LoadLibraryA(path);
	if (library == NULL) {
		snprintf(error, error_size, "LoadLibraryA failed with Windows error %lu",
			(unsigned long)GetLastError());
	}
	return (void *)library;
#else
	void *library = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (library == NULL) {
		aram_set_error(error, error_size, dlerror());
	}
	return library;
#endif
}

static void *aram_library_symbol(void *library, const char *name) {
#ifdef _WIN32
	return (void *)(uintptr_t)GetProcAddress((HMODULE)library, name);
#else
	dlerror();
	return dlsym(library, name);
#endif
}

static void aram_library_discard(void *library) {
	if (library == NULL) {
		return;
	}
#ifdef _WIN32
	FreeLibrary((HMODULE)library);
#else
	dlclose(library);
#endif
}

static int aram_resolve_failed(char *error, size_t error_size, const char *name) {
#ifdef _WIN32
	snprintf(error, error_size, "GetProcAddress(%s) failed with Windows error %lu",
		name, (unsigned long)GetLastError());
#else
	const char *detail = dlerror();
	if (detail != NULL && detail[0] != '\0') {
		snprintf(error, error_size, "dlsym(%s): %s", name, detail);
	} else {
		snprintf(error, error_size, "dlsym(%s) returned no symbol", name);
	}
#endif
	return 0;
}

#define ARAM_RESOLVE(api, field, type, symbol_name, error, error_size) do { \
	void *symbol = aram_library_symbol((api)->library, (symbol_name)); \
	if (symbol == NULL) { \
		aram_resolve_failed((error), (error_size), (symbol_name)); \
		aram_library_discard((api)->library); \
		free(api); \
		return NULL; \
	} \
	(api)->field = (type)(uintptr_t)symbol; \
} while (0)

static aram_unicorn_api *aram_unicorn_load(
	const char *path,
	char *error,
	size_t error_size
) {
	aram_unicorn_api *api = (aram_unicorn_api *)calloc(1, sizeof(*api));
	if (api == NULL) {
		aram_set_error(error, error_size, "allocate Unicorn symbol table: out of memory");
		return NULL;
	}
	api->library = aram_library_open(path, error, error_size);
	if (api->library == NULL) {
		free(api);
		return NULL;
	}
	ARAM_RESOLVE(api, version, aram_uc_version_fn, "uc_version", error, error_size);
	ARAM_RESOLVE(api, open, aram_uc_open_fn, "uc_open", error, error_size);
	ARAM_RESOLVE(api, close, aram_uc_close_fn, "uc_close", error, error_size);
	ARAM_RESOLVE(api, reg_read, aram_uc_reg_read_fn, "uc_reg_read", error, error_size);
	ARAM_RESOLVE(api, reg_write, aram_uc_reg_write_fn, "uc_reg_write", error, error_size);
	ARAM_RESOLVE(api, mem_read, aram_uc_mem_read_fn, "uc_mem_read", error, error_size);
	ARAM_RESOLVE(api, mem_write, aram_uc_mem_write_fn, "uc_mem_write", error, error_size);
	ARAM_RESOLVE(api, mem_map, aram_uc_mem_map_fn, "uc_mem_map", error, error_size);
	ARAM_RESOLVE(api, emu_start, aram_uc_emu_start_fn, "uc_emu_start", error, error_size);
	ARAM_RESOLVE(api, emu_stop, aram_uc_emu_stop_fn, "uc_emu_stop", error, error_size);
	if (!aram_worker_start(api, error, error_size)) {
		aram_library_discard(api->library);
		free(api);
		return NULL;
	}
	return api;
}

static int aram_unicorn_unload(
	aram_unicorn_api *api,
	char *error,
	size_t error_size
) {
	if (api == NULL) {
		return 0;
	}
	void *library = api->library;
	aram_worker_stop(api);
	free(api);
#ifdef _WIN32
	if (library != NULL && !FreeLibrary((HMODULE)library)) {
		snprintf(error, error_size, "FreeLibrary failed with Windows error %lu",
			(unsigned long)GetLastError());
		return -1;
	}
#else
	if (library != NULL && dlclose(library) != 0) {
		aram_set_error(error, error_size, dlerror());
		return -1;
	}
#endif
	return 0;
}

static uint32_t aram_uc_version(
	aram_unicorn_api *api,
	uint32_t *major,
	uint32_t *minor
) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_VERSION;
	aram_dispatch_call(api, &call);
	*major = call.major;
	*minor = call.minor;
	return call.version;
}

static int32_t aram_uc_open(
	aram_unicorn_api *api,
	int32_t architecture,
	int32_t mode,
	uintptr_t *engine
) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_OPEN;
	call.architecture = architecture;
	call.mode = mode;
	aram_dispatch_call(api, &call);
	if (call.result == 0) {
		*engine = call.opened_engine;
	}
	return call.result;
}

static int32_t aram_uc_close(aram_unicorn_api *api, uintptr_t engine) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_CLOSE;
	call.engine = engine;
	aram_dispatch_call(api, &call);
	return call.result;
}

static int32_t aram_uc_reg_read(
	aram_unicorn_api *api,
	uintptr_t engine,
	int32_t register_id,
	void *value
) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_REG_READ;
	call.engine = engine;
	call.register_id = register_id;
	call.data = value;
	aram_dispatch_call(api, &call);
	return call.result;
}

static int32_t aram_uc_reg_write(
	aram_unicorn_api *api,
	uintptr_t engine,
	int32_t register_id,
	const void *value
) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_REG_WRITE;
	call.engine = engine;
	call.register_id = register_id;
	call.data = (void *)value;
	aram_dispatch_call(api, &call);
	return call.result;
}

static int32_t aram_uc_mem_read(
	aram_unicorn_api *api,
	uintptr_t engine,
	uint64_t address,
	void *destination,
	uint64_t size
) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_MEM_READ;
	call.engine = engine;
	call.address = address;
	call.size = size;
	call.data = destination;
	aram_dispatch_call(api, &call);
	return call.result;
}

static int32_t aram_uc_mem_write(
	aram_unicorn_api *api,
	uintptr_t engine,
	uint64_t address,
	const void *source,
	uint64_t size
) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_MEM_WRITE;
	call.engine = engine;
	call.address = address;
	call.size = size;
	call.data = (void *)source;
	aram_dispatch_call(api, &call);
	return call.result;
}

static int32_t aram_uc_mem_map(
	aram_unicorn_api *api,
	uintptr_t engine,
	uint64_t address,
	uint64_t size,
	uint32_t permissions
) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_MEM_MAP;
	call.engine = engine;
	call.address = address;
	call.size = size;
	call.permissions = permissions;
	aram_dispatch_call(api, &call);
	return call.result;
}

static int32_t aram_uc_emu_start(
	aram_unicorn_api *api,
	uintptr_t engine,
	uint64_t begin,
	uint64_t until,
	uint64_t timeout,
	size_t count
) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_EMU_START;
	call.engine = engine;
	call.begin = begin;
	call.until = until;
	call.timeout = timeout;
	call.count = count;
	aram_dispatch_call(api, &call);
	return call.result;
}

static int32_t aram_uc_emu_stop(aram_unicorn_api *api, uintptr_t engine) {
	aram_unicorn_call call = {0};
	call.operation = ARAM_UC_EMU_STOP;
	call.engine = engine;
	aram_dispatch_call(api, &call);
	return call.result;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const loaderErrorSize = 1024

func openUnicornAPI(path string) (*unicornAPI, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var message [loaderErrorSize]C.char
	handle := C.aram_unicorn_load(cPath, &message[0], C.size_t(len(message)))
	if handle == nil {
		return nil, fmt.Errorf("load native library: %s", C.GoString(&message[0]))
	}
	api := &unicornAPI{handle: unsafe.Pointer(handle), path: path}
	var major C.uint32_t
	var minor C.uint32_t
	C.aram_uc_version(handle, &major, &minor)
	api.major = uint32(major)
	api.minor = uint32(minor)
	if api.major != supportedUnicornAPIMajor {
		_ = api.release()
		return nil, fmt.Errorf(
			"Unicorn API %d.%d, want %d.x",
			api.major, api.minor, supportedUnicornAPIMajor,
		)
	}
	return api, nil
}

func (api *unicornAPI) nativeHandle() *C.aram_unicorn_api {
	return (*C.aram_unicorn_api)(api.handle)
}

func (api *unicornAPI) release() error {
	if api == nil || api.handle == nil {
		return nil
	}
	handle := api.nativeHandle()
	api.handle = nil
	var message [loaderErrorSize]C.char
	if C.aram_unicorn_unload(handle, &message[0], C.size_t(len(message))) != 0 {
		return fmt.Errorf("unload native library: %s", C.GoString(&message[0]))
	}
	return nil
}

func (api *unicornAPI) openEngine(architecture, mode int32, engine *uintptr) int32 {
	return int32(C.aram_uc_open(
		api.nativeHandle(),
		C.int32_t(architecture),
		C.int32_t(mode),
		(*C.uintptr_t)(unsafe.Pointer(engine)),
	))
}

func (api *unicornAPI) closeEngine(engine uintptr) int32 {
	return int32(C.aram_uc_close(api.nativeHandle(), C.uintptr_t(engine)))
}

func (api *unicornAPI) readRegister(engine uintptr, register int32, value unsafe.Pointer) int32 {
	return int32(C.aram_uc_reg_read(
		api.nativeHandle(), C.uintptr_t(engine), C.int32_t(register), value,
	))
}

func (api *unicornAPI) writeRegister(engine uintptr, register int32, value unsafe.Pointer) int32 {
	return int32(C.aram_uc_reg_write(
		api.nativeHandle(), C.uintptr_t(engine), C.int32_t(register), value,
	))
}

func (api *unicornAPI) readMemory(
	engine uintptr,
	address uint64,
	destination unsafe.Pointer,
	size uint64,
) int32 {
	return int32(C.aram_uc_mem_read(
		api.nativeHandle(),
		C.uintptr_t(engine),
		C.uint64_t(address),
		destination,
		C.uint64_t(size),
	))
}

func (api *unicornAPI) writeMemory(
	engine uintptr,
	address uint64,
	source unsafe.Pointer,
	size uint64,
) int32 {
	return int32(C.aram_uc_mem_write(
		api.nativeHandle(),
		C.uintptr_t(engine),
		C.uint64_t(address),
		source,
		C.uint64_t(size),
	))
}

func (api *unicornAPI) mapMemory(
	engine uintptr,
	address, size uint64,
	permissions uint32,
) int32 {
	return int32(C.aram_uc_mem_map(
		api.nativeHandle(),
		C.uintptr_t(engine),
		C.uint64_t(address),
		C.uint64_t(size),
		C.uint32_t(permissions),
	))
}

func (api *unicornAPI) start(
	engine uintptr,
	begin, until, timeout uint64,
	count uintptr,
) int32 {
	return int32(C.aram_uc_emu_start(
		api.nativeHandle(),
		C.uintptr_t(engine),
		C.uint64_t(begin),
		C.uint64_t(until),
		C.uint64_t(timeout),
		C.size_t(count),
	))
}

func (api *unicornAPI) stop(engine uintptr) int32 {
	return int32(C.aram_uc_emu_stop(api.nativeHandle(), C.uintptr_t(engine)))
}
