package terminal

import "time"

// Options holds parameters controlling terminal buffering, timeouts, and backpressure.
type Options struct {
	MaxDimension             int
	StagingBufferBytes       int
	OutputHighWaterBytes     int
	OutputLowWaterBytes      int
	BackpressurePollInterval time.Duration
	ControlWriteTimeout      time.Duration
	OutputWriteTimeout       time.Duration
	ExitWriteTimeout         time.Duration
	InputChunkSize           int
}

// DefaultOptions returns standard operational parameters.
func DefaultOptions() Options {
	const (
		defaultMaxDim     = 1000
		defaultStaging    = 2097152 // 2 MiB
		defaultHighWater  = 1048576 // 1 MiB
		defaultLowWater   = 131072  // 128 KiB
		defaultPollInt    = 100 * time.Millisecond
		defaultControlTO  = 5 * time.Second
		defaultOutputTO   = 10 * time.Second
		defaultExitTO     = 3 * time.Second
		defaultInputChunk = 32768 // 32 KiB
	)
	return Options{
		MaxDimension:             defaultMaxDim,
		StagingBufferBytes:       defaultStaging,
		OutputHighWaterBytes:     defaultHighWater,
		OutputLowWaterBytes:      defaultLowWater,
		BackpressurePollInterval: defaultPollInt,
		ControlWriteTimeout:      defaultControlTO,
		OutputWriteTimeout:       defaultOutputTO,
		ExitWriteTimeout:         defaultExitTO,
		InputChunkSize:           defaultInputChunk,
	}
}
