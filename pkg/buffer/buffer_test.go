package buffer

import (
	"sync"
	"testing"

	"go.uber.org/zap"
)

func testLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

func TestNew(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	buf := New[int](10, logger)
	if buf == nil {
		t.Fatal("Expected buffer, got nil")
	}

	if buf.Capacity() != 10 {
		t.Errorf("Expected capacity 10, got %d", buf.Capacity())
	}

	if buf.Size() != 0 {
		t.Errorf("Expected size 0, got %d", buf.Size())
	}
}

func TestAdd_Single(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	buf := New[int](5, logger)
	buf.Add(42)

	if buf.Size() != 1 {
		t.Errorf("Expected size 1, got %d", buf.Size())
	}
}

func TestAdd_Multiple(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	buf := New[string](5, logger)
	items := []string{"a", "b", "c"}

	for _, item := range items {
		buf.Add(item)
	}

	if buf.Size() != 3 {
		t.Errorf("Expected size 3, got %d", buf.Size())
	}
}

func TestAdd_Overflow(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	buf := New[int](3, logger)

	// Add more items than capacity
	for i := 1; i <= 5; i++ {
		buf.Add(i)
	}

	// Size should be capped at capacity
	if buf.Size() != 3 {
		t.Errorf("Expected size 3, got %d", buf.Size())
	}

	// GetAllAndClear should return last 3 items (3, 4, 5)
	items := buf.GetAllAndClear()
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}

	expected := []int{3, 4, 5}
	for i, item := range items {
		if item != expected[i] {
			t.Errorf("Expected item[%d]=%d, got %d", i, expected[i], item)
		}
	}
}

func TestGetAllAndClear_Empty(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	buf := New[int](5, logger)
	items := buf.GetAllAndClear()

	if items != nil {
		t.Errorf("Expected nil for empty buffer, got %v", items)
	}
}

func TestGetAllAndClear_Ordering(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	buf := New[int](5, logger)
	expected := []int{1, 2, 3, 4, 5}

	for _, v := range expected {
		buf.Add(v)
	}

	items := buf.GetAllAndClear()
	if len(items) != len(expected) {
		t.Fatalf("Expected %d items, got %d", len(expected), len(items))
	}

	for i, item := range items {
		if item != expected[i] {
			t.Errorf("Expected item[%d]=%d, got %d", i, expected[i], item)
		}
	}

	// Buffer should be empty after GetAllAndClear
	if buf.Size() != 0 {
		t.Errorf("Expected size 0 after clear, got %d", buf.Size())
	}
}

func TestGetAllAndClear_ClearsBuffer(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	buf := New[int](5, logger)
	buf.Add(1)
	buf.Add(2)
	buf.Add(3)

	_ = buf.GetAllAndClear()

	if buf.Size() != 0 {
		t.Errorf("Expected size 0 after clear, got %d", buf.Size())
	}

	// Adding after clear should work normally
	buf.Add(10)
	if buf.Size() != 1 {
		t.Errorf("Expected size 1 after adding to cleared buffer, got %d", buf.Size())
	}
}

func TestConcurrentAccess(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	buf := New[int](100, logger)
	var wg sync.WaitGroup

	// Multiple goroutines adding
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				buf.Add(val*10 + j)
			}
		}(i)
	}

	wg.Wait()

	// Should have 100 items (or capacity, whichever is smaller)
	size := buf.Size()
	if size != 100 {
		t.Errorf("Expected size 100, got %d", size)
	}

	// GetAllAndClear should work without panic
	items := buf.GetAllAndClear()
	if len(items) != 100 {
		t.Errorf("Expected 100 items, got %d", len(items))
	}

	if buf.Size() != 0 {
		t.Errorf("Expected size 0 after clear, got %d", buf.Size())
	}
}

func TestStats(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	buf := New[int](10, logger)
	buf.Add(1)
	buf.Add(2)
	buf.Add(3)

	size, capacity := buf.Stats()
	if size != 3 {
		t.Errorf("Expected size 3, got %d", size)
	}
	if capacity != 10 {
		t.Errorf("Expected capacity 10, got %d", capacity)
	}
}

func TestGenericTypes(t *testing.T) {
	logger := testLogger()
	defer logger.Sync()

	t.Run("int buffer", func(t *testing.T) {
		buf := New[int](5, logger)
		buf.Add(42)
		items := buf.GetAllAndClear()
		if len(items) != 1 || items[0] != 42 {
			t.Errorf("Expected [42], got %v", items)
		}
	})

	t.Run("string buffer", func(t *testing.T) {
		buf := New[string](5, logger)
		buf.Add("hello")
		items := buf.GetAllAndClear()
		if len(items) != 1 || items[0] != "hello" {
			t.Errorf("Expected [hello], got %v", items)
		}
	})

	t.Run("struct pointer buffer", func(t *testing.T) {
		type TestStruct struct {
			Value int
		}
		buf := New[*TestStruct](5, logger)
		buf.Add(&TestStruct{Value: 123})
		items := buf.GetAllAndClear()
		if len(items) != 1 || items[0].Value != 123 {
			t.Errorf("Expected [{Value:123}], got %v", items)
		}
	})
}
