//go:build !unix

package main

import "errors"

func readControllerState(string) ([]byte, error) {
	return nil, errors.New("verified Actions controller state requires Unix file identity")
}
