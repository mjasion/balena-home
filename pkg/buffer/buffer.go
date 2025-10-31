package buffer

import (
	"sync"

	"go.uber.org/zap"
)

// RingBuffer is a thread-safe generic circular buffer
// It stores items in a circular fashion, overwriting the oldest when full
type RingBuffer[T any] struct {
	mu       sync.RWMutex
	data     []T
	capacity int
	size     int
	head     int
	logger   *zap.Logger
}

// New creates a new generic RingBuffer with the specified capacity
func New[T any](capacity int, logger *zap.Logger) *RingBuffer[T] {
	return &RingBuffer[T]{
		data:     make([]T, capacity),
		capacity: capacity,
		size:     0,
		head:     0,
		logger:   logger,
	}
}

// Add inserts a new item into the buffer
// If the buffer is full, it overwrites the oldest entry
func (rb *RingBuffer[T]) Add(item T) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.size == rb.capacity {
		rb.logger.Warn("ring buffer full, overwriting oldest entry",
			zap.Int("capacity", rb.capacity))
	}

	rb.data[rb.head] = item
	rb.head = (rb.head + 1) % rb.capacity

	if rb.size < rb.capacity {
		rb.size++
	}
}

// GetAllAndClear atomically retrieves all buffered items and clears the buffer
// This is the primary method for consuming buffer contents
// The returned slice is a copy, so it's safe to use after the call
func (rb *RingBuffer[T]) GetAllAndClear() []T {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.size == 0 {
		return nil
	}

	results := make([]T, rb.size)
	if rb.size < rb.capacity {
		// Buffer not full, entries are at indices 0 to size-1
		copy(results, rb.data[:rb.size])
	} else {
		// Buffer is full, need to reconstruct order
		// Oldest is at head, newest is at head-1
		tail := rb.head
		for i := 0; i < rb.size; i++ {
			idx := (tail + i) % rb.capacity
			results[i] = rb.data[idx]
		}
	}

	// Clear the buffer
	rb.size = 0
	rb.head = 0

	return results
}

// Size returns the current number of entries in the buffer
func (rb *RingBuffer[T]) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// Capacity returns the maximum capacity of the buffer
func (rb *RingBuffer[T]) Capacity() int {
	return rb.capacity
}

// Stats returns buffer statistics (size and capacity)
func (rb *RingBuffer[T]) Stats() (size, capacity int) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size, rb.capacity
}
