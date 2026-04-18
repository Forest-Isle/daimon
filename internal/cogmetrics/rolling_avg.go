package cogmetrics

// RollingAvg maintains a windowed average over the last N values.
// Zero value is usable with window size 0 (unbounded, but not recommended).
type RollingAvg struct {
	values []float64
	pos    int
	count  int
	sum    float64
	cap    int
}

// NewRollingAvg creates a RollingAvg with the given window size.
func NewRollingAvg(windowSize int) RollingAvg {
	if windowSize <= 0 {
		windowSize = 100
	}
	return RollingAvg{
		values: make([]float64, windowSize),
		cap:    windowSize,
	}
}

// Add inserts a new value into the rolling window.
func (r *RollingAvg) Add(v float64) {
	if r.count >= r.cap {
		r.sum -= r.values[r.pos]
	} else {
		r.count++
	}
	r.values[r.pos] = v
	r.sum += v
	r.pos = (r.pos + 1) % r.cap
}

// Avg returns the current windowed average. Returns 0 if empty.
func (r *RollingAvg) Avg() float64 {
	if r.count == 0 {
		return 0
	}
	return r.sum / float64(r.count)
}

// Count returns the number of values in the window.
func (r *RollingAvg) Count() int {
	return r.count
}
