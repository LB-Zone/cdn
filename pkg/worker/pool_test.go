package worker

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestWorkerPool(t *testing.T) {
	config := Config{
		Workers:    2,
		QueueSize:  4,
		MaxRetries: 2,
		RetryDelay: time.Millisecond * 10,
	}

	pool := NewPool(config)
	pool.Start()
	defer pool.Stop()

	t.Run("successful job processing", func(t *testing.T) {
		respChan := make(chan error)
		job := Job{
			ID: "test-1",
			Task: func() error {
				return nil
			},
			Response: respChan,
		}

		if err := pool.Submit(job); err != nil {
			t.Errorf("failed to submit job: %v", err)
		}

		select {
		case err := <-respChan:
			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		case <-time.After(time.Second):
			t.Error("job processing timed out")
		}
	})

	t.Run("job with retries", func(t *testing.T) {
		attempts := 0
		respChan := make(chan error)
		job := Job{
			ID: "test-2",
			Task: func() error {
				attempts++
				if attempts <= 1 {
					return errors.New("temporary error")
				}
				return nil
			},
			Response: respChan,
		}

		if err := pool.Submit(job); err != nil {
			t.Errorf("failed to submit job: %v", err)
		}

		select {
		case err := <-respChan:
			if err != nil {
				t.Errorf("expected no error after retry, got %v", err)
			}
			if attempts != 2 {
				t.Errorf("expected 2 attempts, got %d", attempts)
			}
		case <-time.After(time.Second):
			t.Error("job processing timed out")
		}
	})

	t.Run("queue full", func(t *testing.T) {
		// Deterministic: occupy every worker with a job held behind a gate, wait
		// until they have taken them, then fill the queue. Filling while workers
		// were still draining raced them, and unread unbuffered replies left the
		// workers stuck long after the subtest — Stop then waited its full 30 s.
		release := make(chan struct{})
		// Release the held jobs and let the queue drain, so the next subtest
		// starts with an idle pool.
		defer func() {
			close(release)
			deadline := time.Now().Add(2 * time.Second)
			for len(pool.jobQueue) > 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
		}()
		held := func(id string) Job {
			return Job{ID: id, Task: func() error { <-release; return nil }, Response: make(chan error, 1)}
		}

		for i := 0; i < config.Workers; i++ {
			if err := pool.Submit(held(fmt.Sprintf("busy-%d", i))); err != nil {
				t.Fatalf("failed to occupy a worker: %v", err)
			}
		}
		deadline := time.Now().Add(2 * time.Second)
		for len(pool.jobQueue) > 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}

		for i := 0; i < config.QueueSize; i++ {
			if err := pool.Submit(held(fmt.Sprintf("fill-%d", i))); err != nil {
				t.Fatalf("failed to fill the queue: %v", err)
			}
		}

		if err := pool.Submit(held("overflow")); err == nil {
			t.Error("expected error when queue is full, got nil")
		}
	})

	t.Run("shutdown behavior", func(t *testing.T) {
		// Submit a long-running job. Buffered: nothing reads the reply here, and
		// an unbuffered one held the worker — and Stop's 30 s wait — on the send.
		respChan := make(chan error, 1)
		job := Job{
			ID: "long-running",
			Task: func() error {
				time.Sleep(time.Millisecond * 500)
				return nil
			},
			Response: respChan,
		}

		if err := pool.Submit(job); err != nil {
			t.Errorf("failed to submit job: %v", err)
		}

		// Stop the pool immediately
		pool.Stop()

		// Try to submit a new job
		err := pool.Submit(Job{
			ID: "post-shutdown",
			Task: func() error {
				return nil
			},
			Response: make(chan error),
		})

		if err == nil {
			t.Error("expected error when submitting to stopped pool, got nil")
		}
	})
}

// Submitting to a stopped pool must be refused, never panic: Stop used to close
// the queue, and a Submit racing it could pick the send on a closed channel.
// Stop twice must be harmless too — it used to close the queue again.
func TestSubmitAfterStopIsRefusedNotAPanic(t *testing.T) {
	for i := 0; i < 200; i++ {
		pool := NewPool(Config{Workers: 1, QueueSize: 1, RetryDelay: time.Millisecond})
		pool.Start()
		pool.Stop()
		pool.Stop()

		err := pool.Submit(Job{ID: "late", Task: func() error { return nil }, Response: make(chan error, 1)})
		if err == nil {
			t.Fatal("a stopped pool accepted a job")
		}
	}
}
