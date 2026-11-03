package objectstorage

import "time"

type garbageCollector struct {
	db     *objectDB
	remove func(string) error
	stop   chan struct{}
	done   chan struct{}
}

func newGC(db *objectDB, remove func(string) error) *garbageCollector {
	gc := &garbageCollector{
		db: db, remove: remove,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go gc.run()
	return gc
}

func (gc *garbageCollector) close() {
	close(gc.stop)
	<-gc.done
}

func (gc *garbageCollector) run() {
	defer close(gc.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		gc.collect()
		select {
		case <-gc.stop:
			return
		case <-ticker.C:
		}
	}
}

func (gc *garbageCollector) collect() {
	names, err := gc.db.listGCFiles()
	if err != nil {
		return
	}
	deleted := make([]string, 0, len(names))
collecting:
	for _, name := range names {
		select {
		case <-gc.stop:
			break collecting
		default:
		}
		if err := gc.remove(name); err == nil {
			deleted = append(deleted, name)
		}
	}
	_ = gc.db.deleteGCFiles(deleted)
}
