package domain

import "time"

// JobRetryDelay bounds elapsed backoff without expiring any durable work.
func JobRetryDelay(attempt int64) time.Duration {
	if attempt <= 1 {
		return 5 * time.Second
	}
	if attempt == 2 {
		return 30 * time.Second
	}
	return 120 * time.Second
}
