package main

import (
	"log"

	"github.com/niparis/agent-net/internal/featuremap"
)

func main() {
	if err := featuremap.Generate(); err != nil {
		log.Fatal(err)
	}
}
