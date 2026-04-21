package device

import (
	"context"
	"fmt"
	"time"
)

// ContextExecutor - wrapper for execute function with context awareness
type ContextExecutor struct {
	ctx     context.Context
	timeout time.Duration
}

// NewContextExecutor - build executor with context
func NewContextExecutor(ctx context.Context, timeout time.Duration) *ContextExecutor {
	return &ContextExecutor{
		ctx:     ctx,
		timeout: timeout,
	}
}

// Execute - execute function with context monitoring
func (ce *ContextExecutor) Execute(fn func() error) error {
	// first check
	if err := ce.ctx.Err(); err != nil {
		return fmt.Errorf("operation cancelled: %w", err)
	}

	// Channel for result
	done := make(chan error, 1)

	//execute function in separated go routine
	go func() {
		done <- fn()
	}()

	// wait with context or timeout
	timeout := ce.timeout
	if timeout == 0 {
		timeout = 60 * time.Second // default
	}

	select {
	case err := <-done:
		return err
	case <-ce.ctx.Done():
		// Context cancel
		select {
		case err := <-done:
			// done
			return err
		case <-time.After(5 * time.Second):
			//force cancel
			return fmt.Errorf("operation cancelled (timed out)")
		}
	case <-time.After(timeout):
		return fmt.Errorf("operation timeout after %v", timeout)
	}
}
