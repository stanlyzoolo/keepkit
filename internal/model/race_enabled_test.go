//go:build race

package model

// raceEnabled reports that this test binary runs under the race detector.
// Its instrumentation slows string-heavy code several times over, and the
// pathological-input budget test scales its budget by it rather than either
// flaking on CI's shared runners or dropping the guard.
const raceEnabled = true
