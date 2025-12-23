package main

import _ "unsafe"

// Disabled - runtime.schedtrace not accessible in c-archive mode
// go:linkname schedtrace runtime.schedtrace
// func schedtrace(detailed bool)

const (
	SchedtracePeriodSeconds = 1
	SchedtraceTickInterval  = (SchedtracePeriodSeconds * 1000) / TimerIntervalMS // 50 interrupt firings
)

var (
	schedtraceEnabled  = false // Disabled - runtime.schedtrace not accessible in c-archive mode
	schedtraceDetailed = true
	lastSchedtraceTick uint64
)

func startSchedtraceMonitor() {
	if !schedtraceEnabled {
		print("Schedtrace Monitor: disabled\r\n")
		return
	}
	go schedtraceMonitorLoop()
}

func schedtraceMonitorLoop() {
	print("Schedtrace Monitor: started\r\n")
	print("  Will print every ", SchedtraceTickInterval, " timer interrupt firings (")
	print(SchedtracePeriodSeconds, " second)\r\n")

	traceCount := 0
	for tick := range schedtraceTimerChan {
		if tick.Count-lastSchedtraceTick >= SchedtraceTickInterval {
			lastSchedtraceTick = tick.Count
			traceCount++

				print("\r\n───────────────────────────────────────────────\r\n")
			print("Schedtrace #", traceCount, " at interrupt #", tick.Count)
			print(" (", tick.Timestamp/1000000000, "s)\r\n")

			// schedtrace disabled - not accessible in c-archive mode
			print("(Schedtrace output disabled - runtime.schedtrace not accessible)\r\n")
			// schedtrace(schedtraceDetailed)

			print("───────────────────────────────────────────────\r\n\r\n")
		}
	}
}
