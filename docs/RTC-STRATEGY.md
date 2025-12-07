# Real Time Clock (RTC) Strategy for Mazarin

## Hardware Reality

| Hardware | RTC | Notes |
|----------|-----|-------|
| **Raspberry Pi 4** | ❌ No | No battery-backed clock. Time starts at 0 on boot. |
| **Raspberry Pi 5** | ✅ Yes | Has PCF85063A RTC (I2C interface). Battery-backed. |
| **QEMU aarch64-virt** | ✅ Optional | Can emulate RTC if configured (PL031 or similar) |

## Architecture Decision: Abstract RTC Behind Interface

To support both Pi 4 (no RTC) and Pi 5 (with RTC), use a **provider pattern**:

```go
type RTCProvider interface {
    // Initialize RTC hardware
    Init() error
    
    // Get current time since epoch
    GetTime() (uint64, error)
    
    // Set time (if supported)
    SetTime(unixSeconds uint64) error
    
    // Check if RTC is available
    IsAvailable() bool
}

// No-op implementation for Pi 4
type NoRTCProvider struct{}

func (p *NoRTCProvider) Init() error { return nil }
func (p *NoRTCProvider) GetTime() (uint64, error) { return 0, ErrNoRTC }
func (p *NoRTCProvider) SetTime(_ uint64) error { return ErrNoRTC }
func (p *NoRTCProvider) IsAvailable() bool { return false }

// Real implementation for Pi 5
type Pi5RTCProvider struct {
    // I2C interface to PCF85063A
}
```

## Answering Your Question: Do We Need RTC After Boot?

**Short answer**: NO, not for timekeeping after initialization.

### Timeline

**At Boot**:
1. Kernel starts, time is 0 (or unknown)
2. If RTC available: read it, get actual time
3. Start the **ARM Generic Timer** (software clock)
4. System maintains time via generic timer interrupt

**During Runtime**:
- ARM Generic Timer runs continuously (always available, no battery needed)
- Generates interrupts at configurable intervals
- Kernel maintains system clock via these interrupts
- Can synchronize with NTP or other time sources over network (future)

**RTC only needed for**:
- Initial time synchronization at boot
- Detecting stale cached time after system loss
- (Optional) Periodic verification that kernel clock hasn't drifted

### Single-CPU Boot Sequence

```
[1] Boot at 0x200000
    └─ ARM Generic Timer is ticking (always available)
    
[2] If RTC exists (Pi 5)
    └─ Initialize I2C
    └─ Read PCF85063A
    └─ Store: kernel_time_at_boot = rtc_time
    
[3] Start Generic Timer Interrupt
    └─ Set CNTP_TVAL_EL0 for periodic ticks
    └─ Enable GIC IRQ 30
    
[4] Initialize System Clock
    └─ clock_seconds = kernel_time_at_boot (or 0 if no RTC)
    └─ clock_microseconds = 0
    
[5] On each timer interrupt
    └─ Increment clock_seconds and clock_microseconds
    └─ No RTC access needed anymore
    
[6] Time available via:
    └─ gettimeofday() syscall
    └─ System can now boot without RTC present
```

### Why No RTC Needed After Boot

1. **ARM Generic Timer keeps perfect time** - it's a CPU register, always running
2. **No power loss** - kernel runs continuously until shutdown
3. **Network sync is available** - NTP can sync time over network once networking is up
4. **RTC is expensive** - I2C access takes time, uses CPU cycles

### Exception: Hardware That Lost Power

If system is rebooted unexpectedly (Pi 4 powered off and back on):
- Time starts at 0 again
- Could be fixed by:
  - Network time synchronization (NTP)
  - User provides time via syscall
  - Pi 5: RTC stores last known good time

---

## Implementation Strategy for Mazarin

### Phase 1: Abstract Interface (Now)

Create `src/go/mazarin/rtc.go`:

```go
package main

// RTCProvider abstracts RTC hardware
type RTCProvider interface {
    Init() error
    GetTime() (uint64, error)  // Returns seconds since Unix epoch
    IsAvailable() bool
}

var globalRTC RTCProvider

// Initialize with appropriate provider based on hardware
func RTCInit() {
    if isRaspberryPi5() {
        globalRTC = &Pi5RTCProvider{}
    } else {
        globalRTC = &NoRTCProvider{}
    }
    
    if err := globalRTC.Init(); err != nil {
        Log("RTC init failed: %v (continuing with time=0)", err)
    }
}

// Get current time, using RTC if available
func GetBootTime() uint64 {
    if !globalRTC.IsAvailable() {
        return 0
    }
    
    t, err := globalRTC.GetTime()
    if err != nil {
        return 0
    }
    return t
}
```

### Phase 2: Boot Time Initialization

In `kernel.go` during KernelInit:

```go
func KernelInit() {
    // ... other init ...
    
    // Initialize RTC provider (detects hardware automatically)
    RTCInit()
    
    // Get boot time from RTC if available
    bootTime := GetBootTime()
    
    // Initialize system clock
    InitSystemClock(bootTime)
    
    // Start timer interrupts (uses Generic Timer, not RTC)
    TimerInit()
}
```

### Phase 3: System Clock Maintenance (Interrupt-Driven)

Timer interrupt handler (no RTC needed):

```go
var systemClockSeconds uint64 = 0
var systemClockNanoseconds uint64 = 0

// Called every timer interrupt (e.g., 1000 times per second)
func TimerInterruptHandler() {
    // Increment by interval (e.g., 1 millisecond)
    systemClockNanoseconds += TIMER_INTERVAL_NS
    
    if systemClockNanoseconds >= 1_000_000_000 {
        systemClockSeconds++
        systemClockNanoseconds -= 1_000_000_000
    }
    
    // Reload timer for next interrupt
    ReloadTimer()
}

// Syscall to get current time
func GetTime() (seconds, nanoseconds uint64) {
    return systemClockSeconds, systemClockNanoseconds
}
```

### Phase 4: Build-Time RTC Provider Selection

In `Makefile`:

```makefile
# QEMU virt doesn't need RTC handling
QEMU_BUILD := -tags "qemu"

# Raspberry Pi 4 (no RTC)
PI4_BUILD := -tags "rpi4"

# Raspberry Pi 5 (has RTC)
PI5_BUILD := -tags "rpi5"

kernel-qemu.elf: $(GO_FILES)
	GOTOOLCHAIN=local GOARCH=arm64 GOOS=linux go build $(QEMU_BUILD) -o kernel-qemu.elf

kernel-rpi4.elf: $(GO_FILES)
	GOTOOLCHAIN=local GOARCH=arm64 GOOS=linux go build $(PI4_BUILD) -o kernel-rpi4.elf

kernel-rpi5.elf: $(GO_FILES)
	GOTOOLCHAIN=local GOARCH=arm64 GOOS=linux go build $(PI5_BUILD) -o kernel-rpi5.elf
```

In `rtc.go`, use build tags:

```go
//go:build rpi5
// +build rpi5

package main

// Pi 5 RTC implementation
type Pi5RTCProvider struct {
    // I2C bus handle
    i2c *I2CBus
}

func (p *Pi5RTCProvider) Init() error {
    // Initialize I2C and read RTC
}

func (p *Pi5RTCProvider) GetTime() (uint64, error) {
    // Read PCF85063A via I2C
}

func (p *Pi5RTCProvider) IsAvailable() bool {
    return true
}
```

And:

```go
//go:build !rpi5
// +build !rpi5

package main

// No-op RTC for Pi 4 and QEMU
type NoRTCProvider struct{}

func (p *NoRTCProvider) Init() error { return nil }
func (p *NoRTCProvider) GetTime() (uint64, error) { return 0, ErrNoRTC }
func (p *NoRTCProvider) IsAvailable() bool { return false }
```

---

## Timeline Summary

| Phase | What | RTC Used? |
|-------|------|-----------|
| **Boot** | Kernel starts, read RTC if available | YES (once) |
| **Generic Timer Init** | Start ARM Generic Timer interrupts | NO |
| **System Clock Maintained** | Timer interrupt increments clock | NO |
| **Runtime** | Applications use gettimeofday() syscall | NO |
| **Network Sync** | NTP updates clock via syscall (future) | NO |

---

## Key Takeaways

1. **RTC only needed at boot** - for initial time synchronization
2. **ARM Generic Timer takes over** - provides accurate timekeeping indefinitely
3. **No battery required** - after boot, system maintains time without RTC
4. **Isolated behind interface** - code doesn't care if RTC exists
5. **Build-tag separation** - different RTC providers for different hardware

This approach gives you:
- ✅ Flexible: Works on Pi 4 (no RTC), Pi 5 (with RTC), QEMU (optional)
- ✅ Efficient: RTC accessed only once at boot
- ✅ Robust: Generic Timer is always available
- ✅ Future-proof: Easy to add network time sync later


