package batch

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// Every subtest here shares state between the processor callback and the test
// body, and the callback runs on a worker goroutine that `Start` launches. That
// is a data race in the test rather than in `BatchProcessor` — but it is a real
// one, and `go test -race` fails the package for it.
//
// The mutex in each subtest is what makes the assertions mean anything.
// `maxConcurrent` in particular was being compared against a value several
// workers wrote at once, so the limit it claims to check was never reliably
// checked at all.
func TestBatchProcessor(t *testing.T) {
	config := Config{
		BatchSize:     3,
		FlushTimeout:  100 * time.Millisecond,
		MaxConcurrent: 2,
		MaxRetries:    2,
		RetryDelay:    10 * time.Millisecond,
	}

	t.Run("batch processing", func(t *testing.T) {
		var mu sync.Mutex
		processed := make([]BatchItem, 0)
		processor := func(items []BatchItem) []BatchItem {
			mu.Lock()
			processed = append(processed, items...)
			mu.Unlock()
			for i := range items {
				items[i].Success = true
			}
			return items
		}

		bp := NewBatchProcessor(config, processor)
		bp.Start()
		defer bp.Stop()

		// Add items
		for i := 0; i < 5; i++ {
			bp.Add(BatchItem{
				ID:   string(rune(i + 65)), // A, B, C, D, E
				Data: i,
			})
		}

		// Wait for processing
		time.Sleep(200 * time.Millisecond)

		mu.Lock()
		count := len(processed)
		mu.Unlock()
		if count != 5 {
			t.Errorf("expected 5 processed items, got %d", count)
		}
	})

	t.Run("batch size trigger", func(t *testing.T) {
		var mu sync.Mutex
		batchCount := 0
		processor := func(items []BatchItem) []BatchItem {
			mu.Lock()
			batchCount++
			mu.Unlock()
			for i := range items {
				items[i].Success = true
			}
			return items
		}

		bp := NewBatchProcessor(config, processor)
		bp.Start()
		defer bp.Stop()

		// Add exactly one batch worth of items
		for i := 0; i < config.BatchSize; i++ {
			bp.Add(BatchItem{
				ID:   string(rune(i + 65)),
				Data: i,
			})
		}

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		got := batchCount
		mu.Unlock()
		if got != 1 {
			t.Errorf("expected 1 batch, got %d", got)
		}
	})

	t.Run("timeout trigger", func(t *testing.T) {
		var mu sync.Mutex
		batchCount := 0
		processor := func(items []BatchItem) []BatchItem {
			mu.Lock()
			batchCount++
			mu.Unlock()
			for i := range items {
				items[i].Success = true
			}
			return items
		}

		bp := NewBatchProcessor(config, processor)
		bp.Start()
		defer bp.Stop()

		// Add less than batch size
		bp.Add(BatchItem{
			ID:   "A",
			Data: 1,
		})

		time.Sleep(config.FlushTimeout + 50*time.Millisecond)

		mu.Lock()
		got := batchCount
		mu.Unlock()
		if got != 1 {
			t.Errorf("expected 1 batch due to timeout, got %d", got)
		}
	})

	t.Run("concurrent processing limit", func(t *testing.T) {
		var mu sync.Mutex
		processing := 0
		maxConcurrent := 0
		processor := func(items []BatchItem) []BatchItem {
			mu.Lock()
			processing++
			if processing > maxConcurrent {
				maxConcurrent = processing
			}
			mu.Unlock()

			time.Sleep(50 * time.Millisecond)

			mu.Lock()
			processing--
			mu.Unlock()

			for i := range items {
				items[i].Success = true
			}
			return items
		}

		bp := NewBatchProcessor(config, processor)
		bp.Start()
		defer bp.Stop()

		// Add many items to trigger concurrent processing
		for i := 0; i < 10; i++ {
			bp.Add(BatchItem{
				ID:   string(rune(i + 65)),
				Data: i,
			})
		}

		time.Sleep(200 * time.Millisecond)

		mu.Lock()
		peak := maxConcurrent
		mu.Unlock()
		if peak > config.MaxConcurrent {
			t.Errorf("max concurrent processing exceeded limit: got %d, want <= %d",
				peak, config.MaxConcurrent)
		}
	})

	t.Run("retry behavior", func(t *testing.T) {
		var mu sync.Mutex
		attempts := make(map[string]int)
		processor := func(items []BatchItem) []BatchItem {
			for i := range items {
				id := items[i].ID

				mu.Lock()
				attempts[id]++
				seen := attempts[id]
				mu.Unlock()

				if seen <= 1 {
					items[i].Success = false
					items[i].Error = errors.New("temporary error")
				} else {
					items[i].Success = true
				}
			}
			return items
		}

		bp := NewBatchProcessor(config, processor)
		bp.Start()
		defer bp.Stop()

		bp.Add(BatchItem{
			ID:   "retry-test",
			Data: 1,
		})

		time.Sleep(200 * time.Millisecond)

		mu.Lock()
		got := attempts["retry-test"]
		mu.Unlock()
		if got != 2 {
			t.Errorf("expected 2 attempts for retry, got %d", got)
		}
	})
}
