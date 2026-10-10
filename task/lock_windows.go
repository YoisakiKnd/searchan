//go:build windows

package task

import (
	"errors"
	"os"
)

func lockDir(string) (*os.File, error) {
	return nil, errors.New("durable bot server requires Linux or macOS")
}
