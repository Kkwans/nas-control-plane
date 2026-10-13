//go:build !linux

package main

import "errors"

func lockJobDatabase(string) (func(), error) {
	return nil, errors.New("JOB_OWNER_LOCK_PLATFORM_UNSUPPORTED")
}
