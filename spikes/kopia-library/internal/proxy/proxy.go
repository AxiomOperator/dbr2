// Package proxy is a tiny byte-counting TCP proxy. TLS passes through untouched,
// so the counts are real on-the-wire bytes (payload + gRPC/HTTP2 + TLS overhead).
package proxy

import (
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
)

// Proxy forwards every accepted connection to Target and counts bytes.
type Proxy struct {
	Target string

	Up   atomic.Int64 // client -> server
	Down atomic.Int64 // server -> client

	ln net.Listener
	wg sync.WaitGroup
}

// Start listens on addr and serves in the background.
func (p *Proxy) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	p.ln = ln

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.wg.Add(1)
			go p.handle(c)
		}
	}()

	return nil
}

// Snapshot returns (up, down) byte counters.
func (p *Proxy) Snapshot() (int64, int64) { return p.Up.Load(), p.Down.Load() }

// Close stops accepting connections.
func (p *Proxy) Close() error { return p.ln.Close() }

type counter struct {
	w io.Writer
	n *atomic.Int64
}

func (c counter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n.Add(int64(n))
	return n, err
}

func (p *Proxy) handle(client net.Conn) {
	defer p.wg.Done()
	defer client.Close()

	server, err := net.Dial("tcp", p.Target)
	if err != nil {
		log.Printf("proxy: dial %s: %v", p.Target, err)
		return
	}
	defer server.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(counter{server, &p.Up}, client)
		server.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(counter{client, &p.Down}, server)
		client.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}()
	<-done
	<-done
}
