package compositionfixture

import "sync"

// @import { Bean } from "github.com/spice-framework/spice/annotation/core"

// CleanupLog records instance-owned lifecycle evidence.
type CleanupLog struct {
	mu     sync.Mutex
	events []string
}

// NewCleanupLog constructs isolated evidence for one generated application.
//
// @Bean(name="cleanup")
func NewCleanupLog() *CleanupLog {
	return &CleanupLog{}
}

func (log *CleanupLog) record(event string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.events = append(log.events, event)
}

// Snapshot returns a defensive event copy.
func (log *CleanupLog) Snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.events...)
}
