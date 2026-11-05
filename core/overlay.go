package core

import (
	"context"
	"errors"
	"io/fs"
)

// ErrNoOverlays is returned when the application runs without an overlay server.
var ErrNoOverlays = errors.New("overlays are not configured")

// Overlays delivers live data to overlay pages, such as OBS browser sources.
// Each module has its own overlay, addressed by the module name.
type Overlays interface {
	// Mount serves files as the module's overlay page. unmount removes it.
	Mount(module string, files fs.FS) (unmount func(), err error)
	// Push sends a one-time event, such as an alert. Pages that connect later do not see it.
	Push(ctx context.Context, module, event string, data any) error
	// Set stores the latest value for key and sends it. Pages that connect
	// later receive every stored value first.
	Set(ctx context.Context, module, key string, data any) error
}

// Overlay is one module's view of the overlay service.
type Overlay struct {
	backend Overlays
	scope   *scope
	module  string
}

// Mount serves files as the module's overlay page until the module unloads.
func (o Overlay) Mount(files fs.FS) error {
	if o.backend == nil {
		return ErrNoOverlays
	}
	unmount, err := o.backend.Mount(o.module, files)
	if err != nil {
		return err
	}
	return o.scope.own(unmount)
}

func (o Overlay) Push(ctx context.Context, event string, data any) error {
	if o.backend == nil {
		return ErrNoOverlays
	}
	return o.backend.Push(ctx, o.module, event, data)
}

func (o Overlay) Set(ctx context.Context, key string, data any) error {
	if o.backend == nil {
		return ErrNoOverlays
	}
	return o.backend.Set(ctx, o.module, key, data)
}
