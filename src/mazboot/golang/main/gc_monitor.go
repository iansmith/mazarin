package main

import (
	"runtime"
	"sync/atomic"
)

const (
	TimerIntervalMS  = 20
	GCPeriodSeconds  = 120
	GCTickInterval   = (GCPeriodSeconds * 1000) / TimerIntervalMS // 6000 interrupt firings
)

var (
	gcTriggerCount   atomic.Uint64
	lastGCTick       uint64
	gcMonitorEnabled = true
)

func startGCMonitor() {
	if !gcMonitorEnabled {
		print("GC Monitor: disabled\r\n")
		return
	}
	go gcMonitorLoop()
}

func gcMonitorLoop() {
	print("GC Monitor: started\r\n")
	print("  Will trigger GC every ", GCTickInterval, " timer interrupt firings (")
	print(GCPeriodSeconds, " seconds)\r\n")

	for tick := range gcTimerChan {
		// tick.Count = total number of timer interrupt firings
		if tick.Count-lastGCTick >= GCTickInterval {
			lastGCTick = tick.Count
			count := gcTriggerCount.Add(1)

			print("\r\n═══════════════════════════════════════════════\r\n")
			print("GC Monitor: Triggering GC #", count, "\r\n")
			print("  Timer interrupt firings: ", tick.Count, "\r\n")
			print("  Time: ", tick.Timestamp/1000000000, " seconds\r\n")

			var mBefore runtime.MemStats
			runtime.ReadMemStats(&mBefore)
			print("  Heap before: ", mBefore.HeapAlloc/1024, " KB\r\n")

			runtime.GC()

			var mAfter runtime.MemStats
			runtime.ReadMemStats(&mAfter)
			print("  Heap after: ", mAfter.HeapAlloc/1024, " KB\r\n")
			print("  Freed: ", (mBefore.HeapAlloc-mAfter.HeapAlloc)/1024, " KB\r\n")
			print("═══════════════════════════════════════════════\r\n\r\n")
		}
	}
}
