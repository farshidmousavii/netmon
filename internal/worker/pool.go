package worker

import (
	"context"
	"sync"
)

// Job - A job for the worker pool
type Job func(ctx context.Context) error

// Pool - Worker pool to limit concurrent jobs
type Pool struct {
	workers   int
	jobQueue  chan Job
	ctx       context.Context
	wg        sync.WaitGroup
	cancelCtx context.CancelFunc
}

// NewPool - Creating a new worker pool
func NewPool(ctx context.Context, workers int) *Pool {
	if workers <= 0 {
		workers = 5 // default
	}

	poolCtx, cancel := context.WithCancel(ctx)

	pool := &Pool{
		workers:   workers,
		jobQueue:  make(chan Job, workers*2), // buffer = 2x workers
		ctx:       poolCtx,
		cancelCtx: cancel,
	}

	// start workers
	for i := 0; i < workers; i++ {
		pool.wg.Add(1)
		go pool.worker()
	}

	return pool
}

// worker - goroutine that processes jobs
func (p *Pool) worker() {
	defer p.wg.Done()

	for {
		select {
		case job, ok := <-p.jobQueue:
			if !ok {
				return // closed jobQueue
			}
			job(p.ctx)
		case <-p.ctx.Done():
			return
		}
	}
}

// Submit - Adding a job to the pool
func (p *Pool) Submit(job Job) error {
	select {
	case p.jobQueue <- job:
		return nil
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

// Close - Close the pool and wait for all jobs to complete.
func (p *Pool) Close() {
	close(p.jobQueue)
	p.wg.Wait()
}

// Cancel - Cancel all workers
func (p *Pool) Cancel() {
	p.cancelCtx()
	p.wg.Wait()
}
