package redis

import (
	"context"
	"errors"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker/v2"
)

type Store struct {
	ctx    context.Context
	client *redis.Client
	rs     *redsync.Redsync
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

func NewStoreFromClient(client *redis.Client) gobreaker.SharedDataStore {
	return &Store{
		ctx:    context.Background(),
		client: client,
		rs:     redsync.New(goredis.NewPool(client)),
		mutex:  map[string]*redsync.Mutex{},
	}
}

func (rs *Store) Lock(name string) error {
	mutex, ok := rs.mutex[name]
	if ok {
		return mutex.Lock()
	}

	mutex = rs.rs.NewMutex(name, redsync.WithExpiry(5*time.Second))
	rs.mutex[name] = mutex
	return mutex.Lock()
}

func (rs *Store) Unlock(name string) error {
	mutex, ok := rs.mutex[name]
	if ok {
		var err error
		ok, err = mutex.Unlock()
		if ok && err == nil {
			return nil
		}
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
