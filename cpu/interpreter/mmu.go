package interpreter

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

var (
	ErrMMUTranslationFault = errors.New("MMU translation fault")
	ErrMMUDomainFault      = errors.New("MMU domain fault")
	ErrMMUPermissionFault  = errors.New("MMU permission fault")
	ErrMMUExternalAbort    = errors.New("MMU external abort")
)

type mmuFaultKind uint8

const (
	mmuTranslationFault mmuFaultKind = iota
	mmuDomainFault
	mmuPermissionFault
	mmuExternalFault
)

// MMUFault records the architectural short-descriptor fault information. The
// interpreter reports it to the machine until architectural abort delivery is
// attached; CP15 fault registers are updated before the access returns.
type MMUFault struct {
	Address    uint32
	Domain     uint8
	Status     uint8
	Permission cpu.Permissions
	kind       mmuFaultKind
}

func (f *MMUFault) Error() string {
	return fmt.Sprintf(
		"%v at virtual address 0x%08x (domain %d, status 0x%x, permission 0x%x)",
		f.Unwrap(), f.Address, f.Domain, f.Status, f.Permission,
	)
}

func (f *MMUFault) Unwrap() error {
	switch f.kind {
	case mmuDomainFault:
		return ErrMMUDomainFault
	case mmuPermissionFault:
		return ErrMMUPermissionFault
	case mmuExternalFault:
		return ErrMMUExternalAbort
	default:
		return ErrMMUTranslationFault
	}
}

func isExternalAbort(err error) bool {
	var external cpu.ExternalAbortError
	return errors.As(err, &external) && external.ExternalAbort()
}

func (b *Backend) recordExternalAbort(
	address uint32,
	permission cpu.Permissions,
	err error,
) error {
	// The write accessors call this with whatever the bus returned, success
	// included. Answering that here keeps errors.As -- and the interface target
	// it needs -- off the path of every store that works.
	if err == nil {
		return nil
	}
	if !isExternalAbort(err) {
		return err
	}
	return b.recordMMUFault(address, permission, 0, 0x8, mmuExternalFault)
}

type mmuTranslation struct {
	physicalBase uint32
	domain       uint8
	access       uint8
	page         bool
	cacheable    bool
}

func (b *Backend) mmuEnabled() bool {
	return b.cp15.control&1 != 0
}

// mmuTLBEntries sizes the software translation-lookaside buffer. Descriptors
// are cached per 1 KiB of virtual address, so this covers 4 MiB of resident
// mapping - comfortably more than the working set between the flushes a guest
// actually performs, and small enough to stay in cache itself.
const mmuTLBEntries = 4096

type mmuTLBEntry struct {
	translation mmuTranslation
	tag         uint32
	gen         uint32
	valid       bool
}

// invalidateTLB drops every cached descriptor. It bumps the shared mapping
// generation rather than clearing the table, so a flush stays O(1) on a path
// the guest takes at every context switch.
func (b *Backend) invalidateTLB() {
	b.mappingGen++
	b.invalidateInstructionWindow()
	b.invalidateTranslations()
	b.tlbClear()
}

func (b *Backend) translateAddress(address uint32, permission cpu.Permissions) (uint32, error) {
	physical, _, err := b.translateAddressWithAttributes(address, permission)
	return physical, err
}

func (b *Backend) translateAddressWithAttributes(
	address uint32,
	permission cpu.Permissions,
) (uint32, mmuTranslation, error) {
	if !b.mmuEnabled() {
		return address, mmuTranslation{physicalBase: address &^ 0x3ff, cacheable: true}, nil
	}
	modified := address
	if address < 0x02000000 {
		modified |= b.cp15.processID & 0xfe000000
	}
	key := modified >> 10
	table := b.mmuTLBTable
	if table == nil {
		table = new([mmuTLBEntries]mmuTLBEntry)
		b.mmuTLBTable = table
	}
	entry := &table[key&(mmuTLBEntries-1)]
	var translation mmuTranslation
	if entry.valid && entry.gen == b.mappingGen && entry.tag == key {
		translation = entry.translation
	} else {
		var err error
		translation, err = b.walkShortDescriptor(modified, address, permission)
		if err != nil {
			return 0, mmuTranslation{}, err
		}
		entry.translation, entry.tag, entry.gen, entry.valid = translation, key, b.mappingGen, true
	}
	if err := b.checkTranslationAccess(translation, address, permission); err != nil {
		return 0, mmuTranslation{}, err
	}
	return translation.physicalBase | modified&0x3ff, translation, nil
}

func (b *Backend) walkShortDescriptor(modified, address uint32, permission cpu.Permissions) (mmuTranslation, error) {
	firstAddress := b.cp15.translationTableBase&0xffffc000 | (modified>>20)*4
	first, err := b.readPhysical32(firstAddress)
	if err != nil {
		return mmuTranslation{}, fmt.Errorf("MMU first-level descriptor at 0x%08x: %w", firstAddress, err)
	}
	switch first & 3 {
	case 0:
		return mmuTranslation{}, b.recordMMUFault(address, permission, 0, 0x5, mmuTranslationFault)
	case 1: // coarse page table
		domain := uint8(first >> 5 & 0xf)
		secondAddress := first&0xfffffc00 | ((modified>>12)&0xff)*4
		return b.walkPageDescriptor(secondAddress, modified, address, permission, domain, false)
	case 2: // 1 MiB section
		domain := uint8(first >> 5 & 0xf)
		return mmuTranslation{
			physicalBase: first&0xfff00000 | modified&0x000ffc00,
			domain:       domain,
			access:       uint8(first >> 10 & 3),
			cacheable:    first&(1<<3) != 0,
		}, nil
	case 3: // fine page table
		domain := uint8(first >> 5 & 0xf)
		secondAddress := first&0xfffff000 | ((modified>>10)&0x3ff)*4
		return b.walkPageDescriptor(secondAddress, modified, address, permission, domain, true)
	default:
		panic("unreachable first-level descriptor type")
	}
}

func (b *Backend) walkPageDescriptor(
	descriptorAddress, modified, address uint32,
	permission cpu.Permissions,
	domain uint8,
	fine bool,
) (mmuTranslation, error) {
	descriptor, err := b.readPhysical32(descriptorAddress)
	if err != nil {
		return mmuTranslation{}, fmt.Errorf("MMU second-level descriptor at 0x%08x: %w", descriptorAddress, err)
	}
	switch descriptor & 3 {
	case 0:
		return mmuTranslation{}, b.recordMMUFault(address, permission, domain, 0x7, mmuTranslationFault)
	case 1: // 64 KiB large page, with one AP value per 16 KiB subpage
		shift := uint32(4 + (modified>>14&3)*2)
		return mmuTranslation{
			physicalBase: descriptor&0xffff0000 | modified&0x0000fc00,
			domain:       domain,
			access:       uint8(descriptor >> shift & 3),
			page:         true,
			cacheable:    descriptor&(1<<3) != 0,
		}, nil
	case 2: // 4 KiB small page, with one AP value per 1 KiB subpage
		shift := uint32(4 + (modified>>10&3)*2)
		return mmuTranslation{
			physicalBase: descriptor&0xfffff000 | modified&0x00000c00,
			domain:       domain,
			access:       uint8(descriptor >> shift & 3),
			page:         true,
			cacheable:    descriptor&(1<<3) != 0,
		}, nil
	case 3: // 1 KiB tiny page, valid only below a fine first-level entry
		if !fine {
			return mmuTranslation{}, b.recordMMUFault(address, permission, domain, 0x7, mmuTranslationFault)
		}
		return mmuTranslation{
			physicalBase: descriptor & 0xfffffc00,
			domain:       domain,
			access:       uint8(descriptor >> 4 & 3),
			page:         true,
			cacheable:    descriptor&(1<<3) != 0,
		}, nil
	default:
		panic("unreachable second-level descriptor type")
	}
}

func (b *Backend) checkTranslationAccess(
	translation mmuTranslation,
	address uint32,
	permission cpu.Permissions,
) error {
	domainAccess := uint8(b.cp15.domainAccessControl >> (uint32(translation.domain) * 2) & 3)
	status := uint8(0x9)
	if translation.page {
		status = 0xb
	}
	switch domainAccess {
	case 3: // manager: AP checks are disabled
		return nil
	case 1: // client: apply AP and S/R permission controls below
	case 0, 2:
		return b.recordMMUFault(address, permission, translation.domain, status, mmuDomainFault)
	}

	write := permission&cpu.PermissionWrite != 0
	user := b.currentProcessorMode() == processorModeUser
	allowed := false
	switch translation.access {
	case 0:
		systemProtection := b.cp15.control&(1<<8) != 0
		romProtection := b.cp15.control&(1<<9) != 0
		if !user {
			allowed = systemProtection && !romProtection || !systemProtection && romProtection && !write
		}
	case 1:
		allowed = !user
	case 2:
		allowed = !user || !write
	case 3:
		allowed = true
	}
	if allowed {
		return nil
	}
	status = 0xd
	if translation.page {
		status = 0xf
	}
	return b.recordMMUFault(address, permission, translation.domain, status, mmuPermissionFault)
}

func (b *Backend) recordMMUFault(
	address uint32,
	permission cpu.Permissions,
	domain, status uint8,
	kind mmuFaultKind,
) error {
	faultStatus := uint32(status) | uint32(domain)<<4
	if permission&cpu.PermissionExecute != 0 {
		b.cp15.instructionFaultStatus = faultStatus
	} else {
		b.cp15.dataFaultStatus = faultStatus
	}
	b.cp15.faultAddress = address
	return &MMUFault{
		Address: address, Domain: domain, Status: status,
		Permission: permission, kind: kind,
	}
}

func (b *Backend) readPhysical32(address uint32) (uint32, error) {
	var data [4]byte
	if err := b.copyOut(address, data[:], cpu.PermissionRead); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}

func (b *Backend) readVirtual(address uint32, destination []byte, permission cpu.Permissions) error {
	current := address
	remaining := destination
	for len(remaining) > 0 {
		physical, err := b.translateAddress(current, permission)
		if err != nil {
			return err
		}
		count := min(len(remaining), int(0x400-(current&0x3ff)))
		if err := b.copyOut(physical, remaining[:count], permission); err != nil {
			return b.recordExternalAbort(current, permission, err)
		}
		b.noteVirtualData(current, physical, permission)
		b.noteVirtualTLB(current, physical, permission)
		remaining = remaining[count:]
		current += uint32(count)
	}
	return nil
}

func (b *Backend) writeVirtual(address uint32, source []byte, permission cpu.Permissions) error {
	current := address
	remaining := source
	for len(remaining) > 0 {
		physical, err := b.translateAddress(current, permission)
		if err != nil {
			return err
		}
		count := min(len(remaining), int(0x400-(current&0x3ff)))
		if err := b.copyIn(physical, remaining[:count], permission); err != nil {
			return b.recordExternalAbort(current, permission, err)
		}
		if !b.instructionCacheEnabled() {
			b.smcInvalidate(current, uint32(count), cpu.PermissionExecute)
		}
		b.noteVirtualData(current, physical, permission)
		b.noteVirtualTLB(current, physical, permission)
		remaining = remaining[count:]
		current += uint32(count)
	}
	return nil
}

// noteVirtualTLB admits a whole virtual 4 KiB page to the native inline-memory
// path only when all four ARM926 1 KiB translation chunks are permitted,
// physically consecutive, and backed by one observer-free RAM slice. Failed
// speculative probes restore the architectural fault registers: the guest
// completed its actual access successfully, so validating neighbouring chunks
// must not manufacture a visible fault.
func (b *Backend) noteVirtualTLB(address, accessedPhysical uint32, permission cpu.Permissions) {
	if b.tlb == nil || b.directBus == nil || permission&cpu.PermissionExecute != 0 ||
		b.tlbHit(address, permission) {
		return
	}
	// Prove the completed access itself is still direct RAM before speculative
	// translation checks. With an observer armed this fails immediately, so the
	// neighbouring page-table probes below cannot create observable bus reads.
	if _, _, _, ok := b.directData(accessedPhysical, 1, permission); !ok {
		return
	}
	virtualStart := address &^ uint32(tlbPageSize-1)
	dataStatus := b.cp15.dataFaultStatus
	instructionStatus := b.cp15.instructionFaultStatus
	faultAddress := b.cp15.faultAddress
	var slots [tlbPageSize / 0x400]uint32
	var saved [tlbPageSize / 0x400]mmuTLBEntry
	for index, offset := range []uint32{0, 0x400, 0x800, 0xc00} {
		modified := virtualStart + offset
		if modified < 0x02000000 {
			modified |= b.cp15.processID & 0xfe000000
		}
		slots[index] = modified >> 10 & (mmuTLBEntries - 1)
		if b.mmuTLBTable != nil {
			saved[index] = b.mmuTLBTable[slots[index]]
		}
	}
	defer func() {
		b.cp15.dataFaultStatus = dataStatus
		b.cp15.instructionFaultStatus = instructionStatus
		b.cp15.faultAddress = faultAddress
		if b.mmuTLBTable != nil {
			for index, slot := range slots {
				b.mmuTLBTable[slot] = saved[index]
			}
		}
	}()

	var physicalStart uint32
	for offset := uint32(0); offset < tlbPageSize; offset += 0x400 {
		physical, err := b.translateAddress(virtualStart+offset, permission)
		if err != nil {
			return
		}
		if offset == 0 {
			physicalStart = physical
		} else if uint64(physicalStart)+uint64(offset) != uint64(physical) {
			return
		}
	}
	data, offset, _, ok := b.directData(physicalStart, tlbPageSize, permission)
	if !ok {
		return
	}
	b.tlbNoteMapped(
		virtualStart,
		physicalStart,
		physicalStart-uint32(offset),
		data,
		permission,
	)
}
