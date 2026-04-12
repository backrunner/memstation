package runtimeconfig

import (
	"sync/atomic"
	"time"
)

type DurationValue struct {
	nanos atomic.Int64
}

func NewDurationValue(initial time.Duration) *DurationValue {
	value := &DurationValue{}
	value.Store(initial)
	return value
}

func (v *DurationValue) Load() time.Duration {
	if v == nil {
		return 0
	}
	return time.Duration(v.nanos.Load())
}

func (v *DurationValue) Store(value time.Duration) {
	if v == nil {
		return
	}
	v.nanos.Store(int64(value))
}
