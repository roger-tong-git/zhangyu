package utils

import (
	"context"
	"strings"
	"sync"
	"time"
)

type CacheItem[T any] struct {
	version    int64
	Value      T
	Key        string
	cache      *Cache[T]
	expireChan <-chan time.Time
	expireLock sync.Mutex
}

func (c *CacheItem[T]) expire(duration time.Duration) {
	defer c.expireLock.Unlock()
	c.expireLock.Lock()

	curChan := time.After(duration)
	c.expireChan = curChan
	c.version = c.version + 1
	go func(version int64, curChan <-chan time.Time) {
		<-curChan
		if c.version != version {
			return
		}
		go c.cache.Delete(c.Key)
		if c.cache.expireHandler != nil {
			c.cache.expireHandler(c.Key, c.Value)
		}
	}(c.version, curChan)
}

func newCacheItem[T any](ctx context.Context, key string, value T, cache *Cache[T]) *CacheItem[T] {
	item := &CacheItem[T]{Key: key, Value: value, cache: cache}
	return item
}

type Cache[T any] struct {
	cacheMap      map[string]*CacheItem[T]
	mapLocker     sync.Mutex
	expireHandler func(key string, value any)
	Closer
}

func (s *Cache[T]) SetExpireHandler(expireHandler func(key string, value any)) {
	s.expireHandler = expireHandler
}

func (s *Cache[T]) Get(key string) T {
	defer s.mapLocker.Unlock()
	s.mapLocker.Lock()

	key = s.getKey(key)
	if v, ok := s.cacheMap[key]; ok {
		return v.Value
	} else {
		var zero T
		return zero
	}
}

func (s *Cache[T]) getKey(key string) string {
	return strings.TrimSpace(strings.ToLower(key))
}

func (s *Cache[T]) Set(key string, value T) {
	defer s.mapLocker.Unlock()
	s.mapLocker.Lock()

	key = s.getKey(key)

	if v, ok := s.cacheMap[key]; ok {
		v.Value = value
		v.version = v.version + 1
	} else {
		item := newCacheItem(s.Ctx(), key, value, s)
		s.cacheMap[key] = item
	}
}

func (s *Cache[T]) SetExpire(key string, duration time.Duration) bool {
	defer s.mapLocker.Unlock()
	s.mapLocker.Lock()

	key = s.getKey(key)

	if v, ok := s.cacheMap[key]; ok {
		v.expire(duration)
		return true
	} else {
		return false
	}
}

func (s *Cache[T]) SetValue(key string, value T, duration time.Duration) {
	defer s.mapLocker.Unlock()
	s.mapLocker.Lock()

	key = s.getKey(key)

	if v, ok := s.cacheMap[key]; ok {
		v.Value = value
		v.expire(duration)
	} else {
		item := newCacheItem(s.Ctx(), key, value, s)
		s.cacheMap[key] = item
		item.expire(duration)
	}
}

func (s *Cache[T]) Delete(key string) {
	defer s.mapLocker.Unlock()
	s.mapLocker.Lock()
	delete(s.cacheMap, key)
}

func NewCache[T any](ctx context.Context) *Cache[T] {
	cache := &Cache[T]{cacheMap: make(map[string]*CacheItem[T])}
	cache.SetCtx(ctx)
	return cache
}
