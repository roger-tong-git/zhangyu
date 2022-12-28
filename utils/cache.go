package utils

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

type CacheItem[T any] struct {
	Value      T
	Key        string
	cache      *Cache[T]
	expireChan <-chan time.Time
	Closer
}

func (c *CacheItem[T]) removeItem() {
	c.CtxCancel()
}

func (c *CacheItem[T]) expire(duration time.Duration) {
	curChan := time.After(duration)
	c.expireChan = curChan
	go func() {
		<-curChan
		if curChan == c.expireChan {
			go c.cache.Delete(c.Key)
			if c.cache.expireHandler != nil {
				c.cache.expireHandler(c.Key, c.Value)
			}
		}
	}()
}

func newCacheItem[T any](ctx context.Context, key string, value T, cache *Cache[T]) *CacheItem[T] {
	item := &CacheItem[T]{Key: key, Value: value, cache: cache}
	item.SetCtx(ctx)
	item.SetOnClose(func() {
		log.Println("cache.Delete")
		cache.Delete(key)
	})
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
	maxDuration := time.Hour * 24 * 365 * 100

	if v, ok := s.cacheMap[key]; ok {
		v.Value = value
		v.expire(maxDuration)
	} else {
		item := newCacheItem(s.Ctx(), key, value, s)
		s.cacheMap[key] = item
		item.expire(maxDuration)
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
