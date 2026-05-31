package gateway

// rollbackStack tracks cleanup functions to run on init failure.
// Cleanups are executed in LIFO order (last registered, first run).
type rollbackStack struct {
	cleanups []func()
}

func (r *rollbackStack) push(fn func()) {
	r.cleanups = append(r.cleanups, fn)
}

func (r *rollbackStack) run() {
	for i := len(r.cleanups) - 1; i >= 0; i-- {
		r.cleanups[i]()
	}
}
