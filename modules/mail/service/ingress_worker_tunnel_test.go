// TEST_CASES: MAILMOD-T-010
package main

import (
	"io"
	"net"
	"sync"
	"time"
)

// testWorkerTunnel bridges only a test container's loopback listener to its
// explicitly mounted SSH Unix socket. The other side of that socket validates
// the disposable Worker hostname. TLS remains between SourceClient and Worker.
// Closing the returned function terminates all connections before unmounting.
func testWorkerTunnel(listener net.Listener, socket string) func() {
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	closing := false
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closing {
				mu.Unlock()
				client.Close()
				return
			}
			connections[client] = true
			wg.Add(1)
			mu.Unlock()
			go func() {
				defer wg.Done()
				defer func() {
					client.Close()
					mu.Lock()
					delete(connections, client)
					mu.Unlock()
				}()
				remote, err := net.DialTimeout("unix", socket, 5*time.Second)
				if err != nil {
					return
				}
				defer remote.Close()
				_ = client.SetDeadline(time.Now().Add(40 * time.Second))
				_ = remote.SetDeadline(time.Now().Add(40 * time.Second))
				done := make(chan struct{})
				go func() { _, _ = io.Copy(remote, client); remote.Close(); close(done) }()
				_, _ = io.Copy(client, remote)
				client.Close()
				remote.Close()
				<-done
			}()
		}
	}()
	return func() {
		mu.Lock()
		closing = true
		_ = listener.Close()
		for connection := range connections {
			_ = connection.Close()
		}
		mu.Unlock()
		wg.Wait()
	}
}
