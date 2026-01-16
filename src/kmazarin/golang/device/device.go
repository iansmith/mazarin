package device

import "kmazarin/deviceapi"

// Re-export interfaces from deviceapi for convenience
type Discoverable = deviceapi.Discoverable
type Closable = deviceapi.Closable
type ByteStream = deviceapi.ByteStream
type BlockDevice = deviceapi.BlockDevice
type RandomSource = deviceapi.RandomSource
type Clock = deviceapi.Clock
type Display = deviceapi.Display
type InputDevice = deviceapi.InputDevice
type GPIO = deviceapi.GPIO
type InterruptController = deviceapi.InterruptController

// Re-export types
type FramebufferInfo = deviceapi.FramebufferInfo
type PixelFormat = deviceapi.PixelFormat
type InputEvent = deviceapi.InputEvent
type InputEventType = deviceapi.InputEventType
type PinMode = deviceapi.PinMode

// Re-export constants
const (
	PixelFormatRGB888  = deviceapi.PixelFormatRGB888
	PixelFormatRGBA888 = deviceapi.PixelFormatRGBA888
	PixelFormatBGR888  = deviceapi.PixelFormatBGR888
	PixelFormatBGRA888 = deviceapi.PixelFormatBGRA888

	InputEventKey = deviceapi.InputEventKey
	InputEventRel = deviceapi.InputEventRel
	InputEventAbs = deviceapi.InputEventAbs

	PinModeInput  = deviceapi.PinModeInput
	PinModeOutput = deviceapi.PinModeOutput
	PinModeAlt0   = deviceapi.PinModeAlt0
	PinModeAlt1   = deviceapi.PinModeAlt1
)
