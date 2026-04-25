package sys

import (
	"fmt"
	"runtime"
	"time"
)

// StartMemStatsLogger starts a background goroutine that prints
// runtime.MemStats every interval. Used by long-running shepherds
// (linux, fti, maildb, mail-app, rachel) so per-shepherd memory
// growth shows up in the serial log next to other diagnostic output.
//
// The format is one line per snapshot:
//
//	[mem:<tag>] heap=NNNkB sys=NNNkB live=NNN gc=N pauseTotal=Nms numGoroutine=N
//
// Volume: ~1 line per shepherd per interval — negligible UART pressure.
// Recommended interval: 10–30s. Pass a short tag (the shepherd name)
// so different shepherds' lines are easy to grep apart.
func StartMemStatsLogger(tag string, interval time.Duration) {
	go memStatsLoop(tag, interval)
}

func memStatsLoop(tag string, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	var ms runtime.MemStats
	for range t.C {
		runtime.ReadMemStats(&ms)
		live := ms.Mallocs - ms.Frees
		fmt.Printf("[mem:%s] heap=%dkB sys=%dkB live=%d gc=%d pauseTotal=%dms numGoroutine=%d\n",
			tag,
			ms.HeapAlloc/1024,
			ms.Sys/1024,
			live,
			ms.NumGC,
			ms.PauseTotalNs/1_000_000,
			runtime.NumGoroutine(),
		)
	}
}
