// Command proxy is a standalone byte-counting TCP proxy (cmd/q3 embeds the same code).
//
//	go run ./cmd/proxy -listen 127.0.0.1:51516 -target 127.0.0.1:51515
package main

import (
	"flag"
	"log"
	"time"

	"github.com/AxiomOperator/dbr2/spikes/kopia-library/internal/proxy"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:51516", "listen address")
	target := flag.String("target", "127.0.0.1:51515", "upstream address")
	every := flag.Duration("report", 5*time.Second, "report interval")
	flag.Parse()

	p := &proxy.Proxy{Target: *target}
	if err := p.Start(*listen); err != nil {
		log.Fatal(err)
	}
	log.Printf("proxy %s -> %s", *listen, *target)

	for range time.Tick(*every) {
		up, down := p.Snapshot()
		log.Printf("client->server %d bytes, server->client %d bytes", up, down)
	}
}
