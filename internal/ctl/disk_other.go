//go:build !linux

package ctl

import "errors"

func diskFree(string) (int64, error) { return 0, errors.New("free space is only reported on Linux") }
