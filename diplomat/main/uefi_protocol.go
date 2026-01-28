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

	bs := systemTable.BootServices

	// Get LoadedImage protocol for our image
	var loadedImage uintptr
	status := uefiHandleProtocol(
		uintptr(imageHandle),
		uintptr(unsafe.Pointer(&EFI_LOADED_IMAGE_PROTOCOL_GUID)),
		uintptr(unsafe.Pointer(&loadedImage)),
		bs.HandleProtocol,
	)
	if status != EFI_SUCCESS {
		return nil, &blockDevError{"failed to get LoadedImage protocol"}
	}

	// Get the device handle we were loaded from
	deviceHandle := *(*uintptr)(unsafe.Pointer(loadedImage + LoadedImageDeviceHandle))
	if deviceHandle == 0 {
		return nil, &blockDevError{"no device handle in LoadedImage"}
	}

	// Get Block I/O protocol from the device
	var blockIO uintptr
	status = uefiHandleProtocol(
		deviceHandle,
		uintptr(unsafe.Pointer(&EFI_BLOCK_IO_PROTOCOL_GUID)),
		uintptr(unsafe.Pointer(&blockIO)),
		bs.HandleProtocol,
	)
	if status != EFI_SUCCESS {
		return nil, &blockDevError{"failed to get BlockIO protocol"}
	}

	return NewUEFIBlockDevice(blockIO)
}

// uefiHandleProtocol is implemented in assembly
// Calls EFI_BOOT_SERVICES.HandleProtocol using MS x64 ABI
//
// EFI_STATUS HandleProtocol(
//     IN EFI_HANDLE Handle,           // RCX
//     IN EFI_GUID *Protocol,          // RDX
//     OUT VOID **Interface            // R8
// );
func uefiHandleProtocol(handle, protocol, iface, funcPtr uintptr) EFI_STATUS
