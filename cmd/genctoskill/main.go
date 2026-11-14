package main

import (
	"log"

	"github.com/niparis/agent-net/internal/daemon/ctoskill"
)

func main() {
	if err := ctoskill.Generate(); err != nil {
		log.Fatal(err)
	}
}
