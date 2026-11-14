//go:build !linux

package process

import (
	"context"
	"errors"
)

func run(context.Context, Command, Limits) (Result, error) {
	return Result{ExitCode: -1}, errors.New("media process supervision requires the Linux runtime")
}
