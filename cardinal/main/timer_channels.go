package main

import "sync/atomic"

// TimerTick represents a single timer interrupt firing event.
// Sent to monitor goroutines through timer channels.
type TimerTick struct {
	Count     uint64 // How many times timer interrupt has fired
	Timestamp int64  // Current time in nanoseconds
}

// Counter: How many times has timer interrupt fired?
// This counts the number of timer interrupt firings (software ticks),
// NOT the hardware counter (CNTVCT_EL0) which increments 62.5M times per second.
var timerTickCount atomic.Uint64

const timerChannelBuffer = 10

var (
	gcTimerChan         = make(chan TimerTick, timerChannelBuffer)
	scavengerTimerChan  = make(chan TimerTick, timerChannelBuffer)
	schedtraceTimerChan = make(chan TimerTick, timerChannelBuffer)
)

// timerSignalMonitors is called each time timer interrupt fires.
// It increments the interrupt firing counter and sends tick notifications
// to all monitor goroutines via non-blocking channel sends.
//
// Called from timerSignal() (in goroutine.go) which is called from assembly.
//
//go:nosplit
func timerSignalMonitors() {
	now := nanotime()
	count := timerTickCount.Add(1) // One more interrupt firing

	tick := TimerTick{
		Count:     count, // Total interrupt firings
		Timestamp: now,   // Current time
	}

	// Non-blocking sends to all channels
	// If a channel is full, we skip that send (no blocking in interrupt context)
	select {
	case gcTimerChan <- tick:
	default:
	}

	select {
	case scavengerTimerChan <- tick:
	default:
	}

	select {
	case schedtraceTimerChan <- tick:
	default:
	}
}

// getCurrentTick returns the current timer interrupt firing count.
func getCurrentTick() uint64 {
	return timerTickCount.Load()
}
