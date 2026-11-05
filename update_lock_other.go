//go:build !linux

package main

import "os"

func lockUpdate(path string) (func(), error) {
	if err := os.Mkdir(path+".dir", 0700); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(path + ".dir") }, nil
}
