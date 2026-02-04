// diplomat/main/uefi_protocol.go - UEFI protocol location and management
//
// Provides functions to locate UEFI protocols needed for filesystem access.

package main

import (
	"unsafe"
)

// EFI_GUID is a 128-bit GUID used to identify protocols
type EFI_GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// Well-known UEFI protocol GUIDs
var (
	// EFI_LOADED_IMAGE_PROTOCOL_GUID
	EFI_LOADED_IMAGE_PROTOCOL_GUID = EFI_GUID{
		0x5B1B31A1, 0x9562, 0x11d2,
		[8]byte{0x8E, 0x3F, 0x00, 0xA0, 0xC9, 0x69, 0x72, 0x3B},
	}

	// EFI_BLOCK_IO_PROTOCOL_GUID
	EFI_BLOCK_IO_PROTOCOL_GUID = EFI_GUID{
		0x964e5b21, 0x6459, 0x11d2,
		[8]byte{0x8e, 0x39, 0x00, 0xa0, 0xc9, 0x69, 0x72, 0x3b},
	}

	// EFI_SIMPLE_FILE_SYSTEM_PROTOCOL_GUID
	EFI_SIMPLE_FILE_SYSTEM_PROTOCOL_GUID = EFI_GUID{
		0x0964e5b22, 0x6459, 0x11d2,
		[8]byte{0x8e, 0x39, 0x00, 0xa0, 0xc9, 0x69, 0x72, 0x3b},
	}
)

// EFI_LOADED_IMAGE_PROTOCOL offsets
const (
	LoadedImageRevision        = 0   // UINT32
	LoadedImageParentHandle    = 8   // EFI_HANDLE
	LoadedImageSystemTable     = 16  // EFI_SYSTEM_TABLE*
	LoadedImageDeviceHandle    = 24  // EFI_HANDLE - device we were loaded from
	LoadedImageFilePath        = 32  // EFI_DEVICE_PATH_PROTOCOL*
	LoadedImageReserved        = 40  // VOID*
	LoadedImageLoadOptionsSize = 48  // UINT32
	LoadedImageLoadOptions     = 56  // VOID*
	LoadedImageImageBase       = 64  // VOID*
	LoadedImageImageSize       = 72  // UINT64
	LoadedImageImageCodeType   = 80  // EFI_MEMORY_TYPE
	LoadedImageImageDataType   = 88  // EFI_MEMORY_TYPE
	LoadedImageUnload          = 96  // EFI_IMAGE_UNLOAD
)

// EFI_BOOT_SERVICES offsets we need
const (
	// HandleProtocol is at offset 152 in EFI_BOOT_SERVICES
	// (after Hdr + many other functions)
	BootServicesHandleProtocol = 152
)

// GetBootDeviceBlockIO returns the Block I/O protocol for the device we booted from
func GetBootDeviceBlockIO() (*UEFIBlockDevice, error) {
	if systemTable == nil || systemTable.BootServices == nil {
		return nil, &blockDevError{"boot services not available"}
	}

	plat.DebugPortOut('1')

	// Get LoadedImage protocol for our image
	var loadedImage uintptr
	status := plat.HandleProtocol(
		uintptr(imageHandle),
		uintptr(unsafe.Pointer(&EFI_LOADED_IMAGE_PROTOCOL_GUID)),
		uintptr(unsafe.Pointer(&loadedImage)),
	)
	plat.DebugPortOut('2')
	if status != EFI_SUCCESS {
		return nil, &blockDevError{"failed to get LoadedImage protocol"}
	}
	plat.DebugPortOut('3')

	// Get the device handle we were loaded from
	deviceHandle := *(*uintptr)(unsafe.Pointer(loadedImage + LoadedImageDeviceHandle))
	plat.DebugPortOut('4')
	if deviceHandle == 0 {
		return nil, &blockDevError{"no device handle in LoadedImage"}
	}
	plat.DebugPortOut('5')

	// Get Block I/O protocol from the device
	var blockIO uintptr
	status = plat.HandleProtocol(
		deviceHandle,
		uintptr(unsafe.Pointer(&EFI_BLOCK_IO_PROTOCOL_GUID)),
		uintptr(unsafe.Pointer(&blockIO)),
	)
	plat.DebugPortOut('6')
	if status != EFI_SUCCESS {
		return nil, &blockDevError{"failed to get BlockIO protocol"}
	}
	plat.DebugPortOut('7')

	dev, err := NewUEFIBlockDevice(blockIO)
	plat.DebugPortOut('8')
	if err != nil {
		return nil, err
	}
	plat.DebugPortOut('9')
	printString("Block device ready\r\n")
	return dev, nil
}

// uefiHandleProtocol is declared per-architecture:
// - platform_amd64.go: 4 params (handle, protocol, iface, funcPtr)
// - platform_arm64.go: 3 params (handle, protocol, iface)
