package constants

import (
	"reflect"
	"strings"
	"testing"
)

// TestNoTickDenominatedConfigPolicy is the MAZ-172 gate: no config-sourced
// policy value may be denominated in raw ticks. A tick-count knob silently
// changes meaning whenever kernel_tick_rate changes — preempt_after_ticks
// doubled the preemption quantum and halved the time-attribute rate when the
// tick rate was halved. Policy is expressed in wall-clock units (ms, Hz); the
// kernel derives tick counts at InitPreemptConfig time.
func TestNoTickDenominatedConfigPolicy(t *testing.T) {
	tt := reflect.TypeOf(KernelConfig{})
	for i := 0; i < tt.NumField(); i++ {
		tag := tt.Field(i).Tag.Get("toml")
		if strings.HasSuffix(tag, "_ticks") {
			t.Errorf("KernelConfig.%s (toml:%q) is denominated in ticks; express the policy in ms or Hz and derive the tick count in kirq.InitPreemptConfig (MAZ-172)",
				tt.Field(i).Name, tag)
		}
	}
}
