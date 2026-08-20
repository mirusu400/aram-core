package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"image"
	"image/color"
	"image/draw"
	"io"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/minigame"
	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/eads"
	"github.com/mirusu400/aram-core/loader/ktf"
	"github.com/mirusu400/aram-core/loader/raptor"
	"github.com/mirusu400/aram-core/wipi"
)

func (m *Machine) Load(ctx context.Context, source machinecore.Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StateEmpty {
		return fmt.Errorf("load from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := source.Validate(); err != nil {
		return fmt.Errorf("load application: %w", err)
	}
	if source.Size > maxApplicationSize {
		return fmt.Errorf("load %q: source size %d exceeds limit", source.Name, source.Size)
	}

	data, err := io.ReadAll(io.NewSectionReader(source.ReaderAt, 0, source.Size))
	if err != nil {
		return fmt.Errorf("read application at offset 0x0: %w", err)
	}
	if int64(len(data)) != source.Size {
		return fmt.Errorf("read application at offset 0x%x: %w", len(data), io.ErrUnexpectedEOF)
	}
	digest := sha256.Sum256(data)
	actualSHA256 := hex.EncodeToString(digest[:])
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, actualSHA256) {
		return fmt.Errorf(
			"load %q: SHA-256 mismatch: expected %s, got %s",
			source.Name,
			source.SHA256,
			actualSHA256,
		)
	}
	source.SHA256 = actualSHA256
	ktfPackage, ktfErr := ktf.Inspect(data)
	if ktfErr == nil {
		return m.loadKTF(ctx, source, ktfPackage)
	}
	if errors.Is(ktfErr, ktf.ErrProtectedContent) {
		return fmt.Errorf(
			"%w: inspect KTF WIPI package: %v",
			ErrUnsupportedSource,
			ktfErr,
		)
	}
	if !errors.Is(ktfErr, ktf.ErrNotPackage) {
		var formatErr *ktf.FormatError
		if !errors.As(ktfErr, &formatErr) ||
			formatErr.Path != "archive" ||
			!strings.HasPrefix(formatErr.Reason, "invalid ZIP:") {
			return fmt.Errorf("inspect KTF WIPI package: %w", ktfErr)
		}
	}
	raptorPackage, raptorErr := raptor.Inspect(data)
	if raptorErr == nil {
		return m.loadRaptor(ctx, source, raptorPackage)
	}
	if !errors.Is(raptorErr, raptor.ErrNotPackage) {
		var formatErr *raptor.FormatError
		if !errors.As(raptorErr, &formatErr) ||
			formatErr.Path != "archive" ||
			!strings.HasPrefix(formatErr.Reason, "invalid ZIP:") {
			return fmt.Errorf("inspect Raptor WIPI-C package: %w", raptorErr)
		}
	}
	container, err := loader.InspectContainer(data)
	if err != nil || len(container.Images) == 0 {
		if err == nil {
			err = ErrUnsupportedSource
		}
		return fmt.Errorf("inspect WIPI application: %w", err)
	}
	selected := container.Images[0]
	useMinigameRuntime := selected.Name == quirkdb.MinigameQVGAOEM.ImageName &&
		actualSHA256 == quirkdb.MinigameQVGAOEM.DatSHA256
	requiredMemory := uint64(selected.TextSize) +
		uint64(selected.BSSSize) +
		uint64(DefaultStackSize) +
		uint64(wipi.SystemSize) +
		uint64(wipi.TrampolineSize) +
		uint64(guest.HeapSize)
	if useMinigameRuntime {
		requiredMemory += uint64(minigame.ImageHeapSize)
	}
	if requiredMemory > m.memoryLimit {
		return fmt.Errorf(
			"load %q: guest memory %d exceeds limit %d",
			source.Name,
			requiredMemory,
			m.memoryLimit,
		)
	}
	text, err := eads.ExtractText(data, selected)
	if err != nil {
		return fmt.Errorf("extract EADS image: %w", err)
	}

	if err := m.cpu.Map(
		selected.TextBase,
		selected.TextSize,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		return fmt.Errorf("map EADS text: %w", err)
	}
	if err := m.cpu.WriteMemory(selected.TextBase, text); err != nil {
		return fmt.Errorf("copy EADS text: %w", err)
	}
	if err := m.cpu.Map(
		selected.DataBase,
		selected.BSSSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map EADS BSS: %w", err)
	}
	if err := m.cpu.Map(
		DefaultStackBase,
		DefaultStackSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map application stack: %w", err)
	}
	if err := wipirt.MapRuntimeMemory(m.cpu); err != nil {
		return err
	}
	profileID := source.ProfileID
	carrier := "unknown"
	if profileID == "" {
		if useMinigameRuntime {
			profileID = minigame.ProfileID
			carrier = "skt"
		} else {
			profileID = DefaultProfileID
		}
	}
	publicRuntime, err := wipirt.NewRuntimeForProfile(
		m.cpu,
		m.frame,
		profileID,
		carrier,
		32,
		"wipi-c",
		m.fallbackFont,
	)
	if err != nil {
		return fmt.Errorf("initialize public WIPI runtime: %w", err)
	}
	m.wipi = publicRuntime
	publicRuntime.InvokeSync = func(
		callbackContext context.Context,
		callback wipirt.GuestCallback,
	) (uint32, error) {
		_, value, callbackErr := m.invokeWIPICallback(callbackContext, callback)
		return value, callbackErr
	}
	if err := m.installWIPIResources(); err != nil {
		return err
	}
	if useMinigameRuntime {
		runtime, runtimeErr := minigame.NewRuntime(
			m.cpu,
			m.frame,
			publicRuntime,
			selected.DataBase,
			selected.BSSSize,
			selected.GuestEntry(),
		)
		if runtimeErr != nil {
			return fmt.Errorf("initialize MinigameQVGAOEM runtime: %w", runtimeErr)
		}
		m.minigame = runtime
	}

	entry := selected.GuestEntry()
	if err := m.cpu.WriteRegister(cpu.RegisterSP, DefaultStackBase+DefaultStackSize); err != nil {
		return fmt.Errorf("initialize stack pointer: %w", err)
	}
	if err := m.cpu.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1); err != nil {
		return fmt.Errorf("initialize link register: %w", err)
	}
	if err := m.cpu.WriteRegister(cpu.RegisterPC, entry&^uint32(1)); err != nil {
		return fmt.Errorf("initialize entry point: %w", err)
	}
	if err := m.cpu.WriteRegister(cpu.RegisterCPSR, cpu.StatusThumb); err != nil {
		return fmt.Errorf("initialize Thumb execution mode: %w", err)
	}
	initialContext, err := m.cpu.SaveContext()
	if err != nil {
		return fmt.Errorf("capture initial CPU context: %w", err)
	}

	source.ProfileID = profileID
	m.source = source
	m.info = ImageInfo{
		Name:       selected.Name,
		ProfileID:  profileID,
		SourceKind: loader.KindEADS,
		ImageSHA256: imageSHA256(loader.KindEADS, []imageSegment{
			{
				Address:    selected.TextBase,
				Size:       selected.TextSize,
				Writable:   true,
				Executable: true,
				Data:       text,
			},
			{
				Address:  selected.DataBase,
				Size:     selected.BSSSize,
				Writable: true,
			},
		}),
		EntryPoint:  entry,
		Mode:        cpu.ModeThumb,
		TextAddress: selected.TextBase,
		TextSize:    selected.TextSize,
		BSSAddress:  selected.DataBase,
		BSSSize:     selected.BSSSize,
	}
	m.initialText = append([]byte(nil), text...)
	m.initialContext = initialContext
	m.state = machinecore.StateReady
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	return nil
}

func (m *Machine) loadRaptor(
	ctx context.Context,
	source machinecore.Source,
	pkg raptor.Package,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	requiredMemory := raptorrt.RequiredMemory(pkg.Image)
	if requiredMemory > m.memoryLimit {
		return fmt.Errorf(
			"load %q: Raptor guest memory %d exceeds limit %d",
			source.Name,
			requiredMemory,
			m.memoryLimit,
		)
	}
	text, bss, err := raptorrt.PrimarySections(pkg.Image)
	if err != nil {
		return fmt.Errorf("load Raptor image: %w", err)
	}
	if err := raptorrt.MapRaptorImage(m.cpu, pkg.Image); err != nil {
		return err
	}
	if err := m.cpu.Map(
		DefaultStackBase,
		DefaultStackSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map Raptor application stack: %w", err)
	}
	if err := wipirt.MapRuntimeMemory(m.cpu); err != nil {
		return err
	}
	profileID := source.ProfileID
	if profileID == "" {
		profileID = raptorrt.ProfileID
	}
	publicRuntime, err := wipirt.NewRuntimeForProfile(
		m.cpu,
		m.frame,
		profileID,
		"lgt",
		16,
		"lgt-raptor",
		m.fallbackFont,
	)
	if err != nil {
		return fmt.Errorf("initialize public WIPI runtime for Raptor: %w", err)
	}
	m.wipi = publicRuntime
	publicRuntime.InvokeSync = func(
		callbackContext context.Context,
		callback wipirt.GuestCallback,
	) (uint32, error) {
		_, value, callbackErr := m.invokeWIPICallback(callbackContext, callback)
		return value, callbackErr
	}
	m.initialResources = raptorrt.MergeResources(pkg.Resources, m.initialResources)
	if err := m.installWIPIResources(); err != nil {
		return err
	}
	// LGT titles also address their package contents through MC_fs with the
	// shared access mode (e.g. 제노니아1 probes data/*.zt1 with MC_fsIsExist
	// before deciding whether to download them from the carrier server), so
	// expose the JAR contents to the shared filesystem namespace as well.
	publicRuntime.RegisterSharedPackageFiles(pkg.Resources)
	runtime, err := raptorrt.NewRuntime(m.cpu, publicRuntime, pkg)
	if err != nil {
		return err
	}
	runtime.Net = m.raptorNet
	m.raptor = runtime

	for register, value := range map[uint32]uint32{
		cpu.RegisterR0:   raptorrt.KernelBase,
		cpu.RegisterR1:   raptorrt.DletBase,
		cpu.RegisterR2:   raptorrt.WIPICBase,
		cpu.RegisterSP:   DefaultStackBase + DefaultStackSize,
		cpu.RegisterLR:   guest.ReturnSentinel | 1,
		cpu.RegisterPC:   pkg.Image.Entry,
		cpu.RegisterCPSR: cpu.StatusThumb,
	} {
		if err := m.cpu.WriteRegister(register, value); err != nil {
			return fmt.Errorf("initialize Raptor register %d: %w", register, err)
		}
	}
	initialContext, err := m.cpu.SaveContext()
	if err != nil {
		return fmt.Errorf("capture initial Raptor CPU context: %w", err)
	}
	source.ProfileID = profileID
	m.source = source
	m.info = ImageInfo{
		Name:        pkg.Descriptor.AID,
		ProfileID:   profileID,
		SourceKind:  loader.KindRaptor,
		ImageSHA256: raptorImageSHA256(pkg.Image),
		EntryPoint:  pkg.Image.Entry | 1,
		Mode:        cpu.ModeThumb,
		TextAddress: text.Address,
		TextSize:    text.Size,
		BSSAddress:  bss.Address,
		BSSSize:     bss.Size,
	}
	m.initialText = append([]byte(nil), text.Data...)
	m.initialContext = initialContext
	m.state = machinecore.StateReady
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	return nil
}

func (m *Machine) loadKTF(
	ctx context.Context,
	source machinecore.Source,
	pkg ktf.Package,
) error {
	requiredMemory := uint64(len(pkg.Client)) +
		uint64(pkg.BSSSize) +
		uint64(DefaultStackSize) +
		uint64(ktfrt.HostSize) +
		uint64(guest.HeapSize)
	if requiredMemory > m.memoryLimit {
		return fmt.Errorf(
			"load %q: KTF guest memory %d exceeds limit %d",
			source.Name,
			requiredMemory,
			m.memoryLimit,
		)
	}
	profileID := source.ProfileID
	if profileID == "" {
		profileID = ktfrt.ProfileID
	}
	// KTF descriptors normally name the handset screen the title was built for.
	// Apply only exact-package corrections for known mismatched metadata.
	if width, height := ktfrt.EffectiveDisplaySize(pkg); width > 0 &&
		height > 0 {
		if bounds := m.frame.Bounds(); bounds.Dx() != width || bounds.Dy() != height {
			m.frame = image.NewRGBA(image.Rect(0, 0, width, height))
		}
	}
	runtime, err := ktfrt.NewRuntimeForProfile(
		m.cpu,
		pkg,
		m.frame,
		profileID,
		m.fallbackFont,
	)
	if err != nil {
		return err
	}
	runtime.DeferThreads = true
	if err := runtime.MapImageAndHost(); err != nil {
		return err
	}
	result, executable, err := runtime.Bootstrap(ctx)
	if err != nil {
		return fmt.Errorf(
			"bootstrap KTF application at PC 0x%08x after %d instructions: %w",
			result.PC,
			result.Instructions,
			err,
		)
	}
	if err := runtime.Initialize(ctx); err != nil {
		return err
	}
	source.ProfileID = profileID
	m.source = source
	m.info = ImageInfo{
		Name:       pkg.Descriptor.AID,
		ProfileID:  profileID,
		SourceKind: loader.KindKTF,
		ImageSHA256: imageSHA256(loader.KindKTF, []imageSegment{
			{
				Address:    ktfrt.ImageBase,
				Size:       uint32(len(pkg.Client)),
				Writable:   true,
				Executable: true,
				Data:       pkg.Client,
			},
			{
				Address:  ktfrt.ImageBase + uint32(len(pkg.Client)),
				Size:     pkg.BSSSize,
				Writable: true,
			},
		}),
		EntryPoint:  ktfrt.ImageBase | 1,
		Mode:        cpu.ModeThumb,
		TextAddress: ktfrt.ImageBase,
		TextSize:    uint32(len(pkg.Client)),
		BSSAddress:  ktfrt.ImageBase + uint32(len(pkg.Client)),
		BSSSize:     pkg.BSSSize,
	}
	if runtime.Exe.Name != "" {
		m.info.Name = runtime.Exe.Name
	}
	if executable == 0 {
		return errors.New("KTF bootstrap returned a null executable")
	}
	m.ktf = runtime
	m.initialText = append([]byte(nil), pkg.Client...)
	m.state = machinecore.StateReady
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	return nil
}

func (m *Machine) installWIPIResources() error {
	if m.wipi == nil {
		return nil
	}
	if len(m.initialResources) == 0 {
		return nil
	}
	if result := m.wipi.RegisterResources(m.initialResources); result < 0 {
		// Fall back to per-resource registration so the failing entry is named.
		resourceNames := make([]string, 0, len(m.initialResources))
		for name := range m.initialResources {
			resourceNames = append(resourceNames, name)
		}
		sort.Strings(resourceNames)
		for _, name := range resourceNames {
			if r := m.wipi.RegisterResource(name, m.initialResources[name]); r < 0 {
				return fmt.Errorf("register WIPI resource %q: error %d", name, r)
			}
		}
		return fmt.Errorf("register WIPI resources: error %d", result)
	}
	return nil
}
