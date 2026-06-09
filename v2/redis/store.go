package redis

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker/v2"
)

type Store struct {
	ctx    context.Context
	client redis.UniversalClient
	rs     *redsync.Redsync
	mu     sync.Mutex
	mutex  map[string]*redsync.Mutex
}

func NewStore(addr string) gobreaker.SharedDataStore {
	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})
	return &Store{
		ctx:    context.Background(),
		client: client,
		rs:     redsync.New(goredis.NewPool(client)),
		mutex:  map[string]*redsync.Mutex{},
	}
}

func NewStoreFromClient(client redis.UniversalClient) gobreaker.SharedDataStore {
	return &Store{
		ctx:    context.Background(),
		client: client,
		rs:     redsync.New(goredis.NewPool(client)),
		mutex:  map[string]*redsync.Mutex{},
	}
}

func (rs *Store) Lock(name string) error {
	rs.mu.Lock()
	mutex, ok := rs.mutex[name]
	if !ok {
		mutex = rs.rs.NewMutex(name, redsync.WithExpiry(5*time.Second))
		rs.mutex[name] = mutex
	}
	rs.mu.Unlock()

	return mutex.Lock()
}

func (rs *Store) Unlock(name string) error {
	rs.mu.Lock()
	mutex, ok := rs.mutex[name]
	rs.mu.Unlock()

	if !ok {
		return errors.New("unlock failed")
	}

	success, err := mutex.Unlock()
	if success && err == nil {
		rs.mu.Lock()
		delete(rs.mutex, name)
		rs.mu.Unlock()
		return nil
	}
	return errors.New("unlock failed")
}

func (rs *Store) GetData(name string) ([]byte, error) {
	return rs.client.Get(rs.ctx, name).Bytes()
}

func (rs *Store) SetData(name string, data []byte) error {
	return rs.client.Set(rs.ctx, name, data, 0).Err()
}

func (rs *Store) Close() {
	rs.client.Close()
}
