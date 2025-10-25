package buffer

import (
	"sync"
	"time"

	"github.com/mjasion/balena-home/pstryk_metric/scraper"
	"go.uber.org/zap"
)

// RingBuffer is a thread-safe circular buffer for storing scrape results
type RingBuffer struct {
	mu       sync.RWMutex
	data     []*scraper.ScrapeResult
	capacity int
	size     int
	head     int
	logger   *zap.Logger
}

// New creates a new RingBuffer with the specified capacity
func New(capacity int, logger *zap.Logger) *RingBuffer {
	return &RingBuffer{
		data:     make([]*scraper.ScrapeResult, capacity),
		capacity: capacity,
		size:     0,
		head:     0,
		logger:   logger,
	}
}

// Add inserts a new scrape result into the buffer
// If the buffer is full, it overwrites the oldest entry
func (rb *RingBuffer) Add(result *scraper.ScrapeResult) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.size == rb.capacity {
		rb.logger.Warn("Ring buffer full, dropping oldest entry")
	}

	rb.data[rb.head] = result
	rb.head = (rb.head + 1) % rb.capacity

	if rb.size < rb.capacity {
		rb.size++
	}
}

// GetAll returns all buffered scrape results
func (rb *RingBuffer) GetAll() []*scraper.ScrapeResult {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size == 0 {
		return nil
	}

	results := make([]*scraper.ScrapeResult, rb.size)

	// If buffer is not full, entries are at indices 0 to size-1
	if rb.size < rb.capacity {
		copy(results, rb.data[:rb.size])
	} else {
		// If buffer is full, need to reconstruct order
		// Oldest is at head, newest is at head-1
		tail := rb.head
		for i := 0; i < rb.size; i++ {
			idx := (tail + i) % rb.capacity
			results[i] = rb.data[idx]
		}
	}

	return results
}

// Clear removes all entries from the buffer
func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.size = 0
	rb.head = 0
}

// Size returns the current number of entries in the buffer
func (rb *RingBuffer) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// GetLastScrapeTime returns the timestamp of the most recent scrape
func (rb *RingBuffer) GetLastScrapeTime() time.Time {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size == 0 {
		return time.Time{}
	}

	// Most recent is at head-1
	idx := (rb.head - 1 + rb.capacity) % rb.capacity
	return rb.data[idx].Timestamp
}
