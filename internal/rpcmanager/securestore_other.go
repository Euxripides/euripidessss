//go:build !windows

package rpcmanager

import "errors"

func protectLocal([]byte) ([]byte, error) {
	return nil, errors.New("RPC secure store requires Windows DPAPI")
}

func unprotectLocal([]byte) ([]byte, error) {
	return nil, errors.New("RPC secure store requires Windows DPAPI")
}
