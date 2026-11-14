// Package process owns bounded execution of media tools and their process group.
package process

import (
	"context"
	"errors"
	"time"
)

var ErrDescendants = errors.New("media command left descendants after its leader exited")
var ErrOutputLimit = errors.New("media command stdout exceeded its bounded capture")

type Command struct {
	Path      string
	Args, Env []string
	Dir       string
}
type Limits struct {
	StdoutBytes, StderrBytes  int
	GracePeriod, DrainTimeout time.Duration
}
type Result struct {
	Stdout, Stderr                   []byte
	StdoutTruncated, StderrTruncated bool
	ExitCode                         int
}

func DefaultLimits() Limits {
	return Limits{StdoutBytes: 2 * 1024 * 1024, StderrBytes: 64 * 1024, GracePeriod: 2 * time.Second, DrainTimeout: 2 * time.Second}
}
func Run(ctx context.Context, command Command, limits Limits) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{ExitCode: -1}, err
	}
	if command.Path == "" || limits.StdoutBytes < 1 || limits.StdoutBytes > 16*1024*1024 || limits.StderrBytes < 1 || limits.StderrBytes > 1024*1024 || limits.GracePeriod <= 0 || limits.GracePeriod > 30*time.Second || limits.DrainTimeout <= 0 || limits.DrainTimeout > 30*time.Second {
		return Result{ExitCode: -1}, errors.New("invalid bounded media command")
	}
	return run(ctx, command, limits)
}

type capture struct {
	data            []byte
	limit           int
	tail, truncated bool
}

func (c *capture) Write(p []byte) (int, error) {
	size := len(p)
	if len(c.data)+size > c.limit {
		c.truncated = true
	}
	if c.tail {
		if size >= c.limit {
			c.data = append(c.data[:0], p[size-c.limit:]...)
		} else {
			if excess := len(c.data) + size - c.limit; excess > 0 {
				copy(c.data, c.data[excess:])
				c.data = c.data[:len(c.data)-excess]
			}
			c.data = append(c.data, p...)
		}
	} else {
		remaining := c.limit - len(c.data)
		if len(p) > remaining {
			p = p[:remaining]
		}
		c.data = append(c.data, p...)
	}
	return size, nil
}
