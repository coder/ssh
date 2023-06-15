package main

import (
	"log"
	"time"

	"github.com/gliderlabs/ssh"
)

var (
	keepAliveInterval = 3 * time.Second
	keepAliveCountMax = 3
)

func main() {
	ssh.Handle(func(s ssh.Session) {
		log.Println("new connection")
		i := 0
		for {
			i += 1
			log.Println("active seconds:", i)
			select {
			case <-time.After(time.Second):
				continue
			case <-s.Context().Done():
				log.Println("connection closed")
				return
			}
		}
	})

	log.Println("starting ssh server on port 2222...")
	log.Printf("keep-alive mode is on: %s\n", keepAliveInterval)
	server := &ssh.Server{
		Addr:                ":2222",
		ClientAliveInterval: keepAliveInterval,
		ClientAliveCountMax: keepAliveCountMax,
	}
	log.Fatal(server.ListenAndServe())
}
