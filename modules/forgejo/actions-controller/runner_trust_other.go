//go:build !unix

package main

func readRunnerTrustFile(string, int) ([]byte, error) { return nil, errRunnerTrust }
