//go:build !unix

package main

import "context"

func checkBootstrapKeyringAt(context.Context, string, int) error { return errDebianBootstrapTrust }
