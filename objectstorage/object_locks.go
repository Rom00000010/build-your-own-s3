package objectstorage

import "sync"

type objectLockKey struct {
	bucket string
	key    string
}

type objectLockEntry struct {
	mu    sync.RWMutex
	users int // 包括等待锁的请求，防止尚有等待者时回收锁项。
}

type objectLockTable struct {
	mu      sync.Mutex
	entries map[objectLockKey]*objectLockEntry
}

func (table *objectLockTable) acquire(bucket, key string, write bool) func() {
	id := objectLockKey{bucket: bucket, key: key}
	table.mu.Lock()
	if table.entries == nil {
		table.entries = make(map[objectLockKey]*objectLockEntry)
	}
	entry := table.entries[id]
	if entry == nil {
		entry = &objectLockEntry{}
		table.entries[id] = entry
	}
	entry.users++
	table.mu.Unlock()

	if write {
		entry.mu.Lock()
	} else {
		entry.mu.RLock()
	}
	return func() {
		if write {
			entry.mu.Unlock()
		} else {
			entry.mu.RUnlock()
		}
		table.mu.Lock()
		entry.users--
		if entry.users == 0 {
			delete(table.entries, id)
		}
		table.mu.Unlock()
	}
}
