package wasm

import (
	"context"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type capabilityCtxKey struct{}

// InstancePool manages a pool of pre-instantiated WASM module instances.
type InstancePool struct {
	runtime     wazero.Runtime
	module      wazero.CompiledModule
	caps        *CapabilitySet
	instances   chan api.Module
	maxSize     int
	currentSize int
	mu          sync.Mutex
}

// NewInstancePool creates a pool with pre-warmed instances.
func NewInstancePool(ctx context.Context, runtime wazero.Runtime, module wazero.CompiledModule, caps *CapabilitySet, maxSize int) (*InstancePool, error) {
	pool := &InstancePool{
		runtime:   runtime,
		module:    module,
		caps:      caps,
		instances: make(chan api.Module, maxSize),
		maxSize:   maxSize,
	}

	warmCount := maxSize / 2
	if warmCount < 1 {
		warmCount = 1
	}

	for i := 0; i < warmCount; i++ {
		inst, err := pool.newInstance(ctx)
		if err != nil {
			pool.Close(ctx)
			return nil, fmt.Errorf("warm instance %d: %w", i, err)
		}
		pool.instances <- inst
		pool.currentSize++
	}

	return pool, nil
}

func (p *InstancePool) newInstance(ctx context.Context) (api.Module, error) {
	cfg := wazero.NewModuleConfig().
		WithSysNanotime().
		WithSysWalltime()

	// Attach capabilities to context
	ctx = context.WithValue(ctx, capabilityCtxKey{}, p.caps)

	// Instantiate with WASI support
	inst, err := p.runtime.InstantiateModule(ctx, p.module, cfg)
	if err != nil {
		return nil, fmt.Errorf("instantiate: %w", err)
	}

	return inst, nil
}

// Acquire gets an available instance or creates a new one.
func (p *InstancePool) Acquire(ctx context.Context) (api.Module, error) {
	select {
	case inst := <-p.instances:
		return inst, nil
	default:
	}

	p.mu.Lock()
	if p.currentSize < p.maxSize {
		inst, err := p.newInstance(ctx)
		if err != nil {
			p.mu.Unlock()
			return nil, err
		}
		p.currentSize++
		p.mu.Unlock()
		return inst, nil
	}
	p.mu.Unlock()

	// Pool full — block until one is returned
	select {
	case inst := <-p.instances:
		return inst, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Release returns an instance to the pool or closes it if pool is full.
func (p *InstancePool) Release(inst api.Module) {
	if inst == nil {
		return
	}
	select {
	case p.instances <- inst:
	default:
		inst.Close(context.Background())
	}
}

// Close destroys all pool instances.
func (p *InstancePool) Close(ctx context.Context) {
	for {
		select {
		case inst := <-p.instances:
			inst.Close(ctx)
		default:
			return
		}
	}
}

// ensureWASI instantiates WASI snapshot_preview1 for the runtime.
// Called once per PluginHost.
func ensureWASI(ctx context.Context, runtime wazero.Runtime) error {
	_, err := wasi_snapshot_preview1.Instantiate(ctx, runtime)
	return err
}
