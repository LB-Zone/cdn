package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mstgnz/cdn/pkg/config"
	"github.com/mstgnz/cdn/pkg/observability"
	"github.com/rs/zerolog"
)

// Job represents a task to be processed
type Job struct {
	ID       string
	Task     func() error
	Response chan error
}

// Pool represents a worker pool
type Pool struct {
	workers    int
	jobQueue   chan Job
	logger     zerolog.Logger
	wg         sync.WaitGroup
	ctx        context.Context
	cancelFunc context.CancelFunc
	maxRetries int
	retryDelay time.Duration
	stopOnce   sync.Once
}

// Config represents worker pool configuration
type Config struct {
	Workers    int
	QueueSize  int
	MaxRetries int
	RetryDelay time.Duration
}

// DefaultConfig returns default configuration
func DefaultConfig() Config {
	return Config{
		Workers:    config.GetEnvAsIntOrDefault("WORKER_POOL_SIZE", 5),
		QueueSize:  config.GetEnvAsIntOrDefault("WORKER_QUEUE_SIZE", 10),
		MaxRetries: config.GetEnvAsIntOrDefault("WORKER_MAX_RETRIES", 3),
		RetryDelay: time.Duration(config.GetEnvAsIntOrDefault("WORKER_RETRY_DELAY_MS", 1000)) * time.Millisecond,
	}
}

// NewPool creates a new worker pool
func NewPool(config Config) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{
		workers:    config.Workers,
		jobQueue:   make(chan Job, config.QueueSize),
		logger:     observability.Logger(),
		ctx:        ctx,
		cancelFunc: cancel,
		maxRetries: config.MaxRetries,
		retryDelay: config.RetryDelay,
	}
}

// Start initializes and starts the worker pool
func (p *Pool) Start() {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// Stop gracefully shuts down the worker pool. It is safe to call more than
// once: a second call used to close the job queue again and panic with "close
// of closed channel", which is what a shutdown path that stops twice does.
func (p *Pool) Stop() {
	p.stopOnce.Do(p.stop)
}

func (p *Pool) stop() {
	p.cancelFunc()

	// Wait for all jobs to complete with timeout
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info().Msg("Worker pool stopped gracefully")
	case <-time.After(30 * time.Second):
		p.logger.Warn().Msg("Worker pool stop timed out")
	}

	// The queue is deliberately left open. Workers already exit on the
	// cancelled context, and closing it let a Submit racing the shutdown pick
	// the send case on a closed channel and panic the process.
}

// Submit adds a new job to the pool
func (p *Pool) Submit(job Job) error {
	// Checked first: a select with both cases ready picks one at random, so a
	// stopped pool could otherwise still accept work nobody will ever run.
	if p.ctx.Err() != nil {
		return fmt.Errorf("worker pool is shutting down")
	}
	select {
	case p.jobQueue <- job:
		return nil
	case <-p.ctx.Done():
		return fmt.Errorf("worker pool is shutting down")
	default:
		return fmt.Errorf("job queue is full")
	}
}

// worker processes jobs from the queue
func (p *Pool) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case job, ok := <-p.jobQueue:
			if !ok {
				return
			}

			// Inc/Dec around the job, not a defer: this loop never returns
			// between jobs, so a deferred Dec only ran when the worker exited
			// and the gauge climbed with every job processed.
			observability.WorkerPoolActiveWorkers.Inc()

			var err error
			retries := 0
			start := time.Now()

			for retries <= p.maxRetries {
				err = job.Task()
				duration := time.Since(start).Seconds()

				if err == nil {
					observability.WorkerJobProcessingDuration.WithLabelValues("success").Observe(duration)
					break
				}

				retries++
				observability.WorkerJobRetries.WithLabelValues("image_processing").Inc()

				p.logger.Error().
					Err(err).
					Str("jobID", job.ID).
					Int("workerID", id).
					Int("retry", retries).
					Msg("Job processing failed")

				if retries <= p.maxRetries {
					time.Sleep(p.retryDelay)
					continue
				}

				observability.WorkerJobProcessingDuration.WithLabelValues("failure").Observe(duration)
			}

			observability.WorkerPoolActiveWorkers.Dec()
			job.Response <- err

			// Update queue size metric
			queueSize := float64(len(p.jobQueue))
			observability.WorkerPoolQueueSize.Set(queueSize)

		case <-p.ctx.Done():
			return
		}
	}
}
