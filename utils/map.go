package utils

import "sync"

type SyncMap[T any] struct {
	locker *sync.Mutex
	dict   *map[string]any
}

func NewSyncMap[T any]() *SyncMap[T] {
	return &SyncMap[T]{}
}

func (s *SyncMap[T]) getLocker() *sync.Mutex {
	if s.locker == nil {
		s.locker = &sync.Mutex{}
	}
	return s.locker
}

func (s *SyncMap[T]) getMap() *map[string]any {
	if s.dict == nil {
		s.dict = &map[string]any{}
	}
	return s.dict
}

func (s *SyncMap[T]) GetValue(key string) T {
	defer s.getLocker().Unlock()
	s.getLocker().Lock()

	if v, ok := (*s.getMap())[key]; ok {
		return v.(T)
	} else {
		var zero T
		return zero
	}
}

func (s *SyncMap[T]) PutValue(key string, value T) {
	defer s.getLocker().Unlock()
	s.getLocker().Lock()

	(*s.getMap())[key] = value
}

func (s *SyncMap[T]) Delete(key string) {
	defer s.getLocker().Unlock()
	s.getLocker().Lock()

	delete(*s.getMap(), key)
}
