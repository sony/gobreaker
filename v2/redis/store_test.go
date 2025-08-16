package redis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRedis() (*miniredis.Miniredis, *RedisStore) {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	store := &RedisStore{
		ctx:    context.Background(),
		client: client,
		rs:     redsync.New(goredis.NewPool(client)),
		mutex:  map[string]*redsync.Mutex{},
	}

	return mr, store
}

func TestNewRedisStore(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store := NewRedisStore(mr.Addr())
	assert.NotNil(t, store)

	// Test that it implements the interface
	var _ gobreaker.SharedDataStore = store
}

func TestNewRedisStoreFromClient(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	store := NewRedisStoreFromClient(client)
	assert.NotNil(t, store)

	// Test that it implements the interface
	var _ gobreaker.SharedDataStore = store
}

func TestRedisStore_SetData_GetData(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test setting and getting data
	testData := []byte("test data")
	err := store.SetData("test-key", testData)
	assert.NoError(t, err)

	retrievedData, err := store.GetData("test-key")
	assert.NoError(t, err)
	assert.Equal(t, testData, retrievedData)

	// Test getting non-existent key
	emptyData, err := store.GetData("non-existent")
	assert.Error(t, err)
	assert.Nil(t, emptyData)
}

func TestRedisStore_SetData_GetData_Empty(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test setting empty data
	err := store.SetData("empty-key", []byte{})
	assert.NoError(t, err)

	retrievedData, err := store.GetData("empty-key")
	assert.NoError(t, err)
	assert.Equal(t, []byte{}, retrievedData)
}

func TestRedisStore_SetData_GetData_LargeData(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test with large data
	largeData := make([]byte, 1024*1024) // 1MB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	err := store.SetData("large-key", largeData)
	assert.NoError(t, err)

	retrievedData, err := store.GetData("large-key")
	assert.NoError(t, err)
	assert.Equal(t, largeData, retrievedData)
}

func TestRedisStore_SetData_GetData_SpecialCharacters(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test with special characters in key and data
	specialKey := "test:key:with:colons"
	specialData := []byte("data with spaces and \n newlines \t tabs")

	err := store.SetData(specialKey, specialData)
	assert.NoError(t, err)

	retrievedData, err := store.GetData(specialKey)
	assert.NoError(t, err)
	assert.Equal(t, specialData, retrievedData)
}

func TestRedisStore_Lock_Unlock(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test basic lock/unlock
	err := store.Lock("test-mutex")
	assert.NoError(t, err)

	err = store.Unlock("test-mutex")
	assert.NoError(t, err)
}

func TestRedisStore_Lock_Unlock_MultipleKeys(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test multiple different mutex keys
	mutexKeys := []string{"mutex1", "mutex2", "mutex3"}

	for _, key := range mutexKeys {
		err := store.Lock(key)
		assert.NoError(t, err)

		err = store.Unlock(key)
		assert.NoError(t, err)
	}
}

func TestRedisStore_Lock_Unlock_SameKeyMultipleTimes(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test locking and unlocking the same key multiple times
	key := "repeated-mutex"

	for i := 0; i < 5; i++ {
		err := store.Lock(key)
		assert.NoError(t, err)

		err = store.Unlock(key)
		assert.NoError(t, err)
	}
}

func TestRedisStore_Unlock_WithoutLock(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test unlocking without first locking
	err := store.Unlock("unlocked-mutex")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unlock failed")
}

func TestRedisStore_Concurrent_Operations(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test concurrent set/get operations
	const numGoroutines = 10
	const numOperations = 100

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			for j := 0; j < numOperations; j++ {
				key := fmt.Sprintf("concurrent-key-%d-%d", id, j)
				data := []byte(fmt.Sprintf("data-%d-%d", id, j))

				err := store.SetData(key, data)
				assert.NoError(t, err)

				retrievedData, err := store.GetData(key)
				assert.NoError(t, err)
				assert.Equal(t, data, retrievedData)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

func TestRedisStore_Concurrent_Locks(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()
	defer store.Close()

	// Test concurrent lock/unlock operations
	const numGoroutines = 5
	const numOperations = 50

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			for j := 0; j < numOperations; j++ {
				key := fmt.Sprintf("concurrent-mutex-%d-%d", id, j)

				err := store.Lock(key)
				assert.NoError(t, err)

				// Simulate some work
				time.Sleep(1 * time.Millisecond)

				err = store.Unlock(key)
				assert.NoError(t, err)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

func TestRedisStore_Close(t *testing.T) {
	mr, store := setupTestRedis()
	defer mr.Close()

	// Test that Close doesn't panic
	assert.NotPanics(t, func() {
		store.Close()
	})

	// Test that Close can be called multiple times
	assert.NotPanics(t, func() {
		store.Close()
	})
}

func TestRedisStore_Integration_WithDistributedCircuitBreaker(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store := NewRedisStore(mr.Addr())
	defer func() {
		if rs, ok := store.(*RedisStore); ok {
			rs.Close()
		}
	}()

	// Test that the store works with the distributed circuit breaker
	dcb, err := gobreaker.NewDistributedCircuitBreaker[any](store, gobreaker.Settings{
		Name:        "TestBreaker",
		MaxRequests: 3,
		Interval:    time.Second,
		Timeout:     time.Second * 2,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, dcb)

	// Test basic execution
	result, err := dcb.Execute(func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "success", result)
}

func TestRedisStore_Error_Handling(t *testing.T) {
	// Test with invalid Redis address
	store := NewRedisStore("invalid-address:6379")
	defer func() {
		if rs, ok := store.(*RedisStore); ok {
			rs.Close()
		}
	}()

	// These operations should fail due to connection issues
	err := store.SetData("test", []byte("data"))
	assert.Error(t, err)

	_, err = store.GetData("test")
	assert.Error(t, err)

	err = store.Lock("test")
	assert.Error(t, err)
}
