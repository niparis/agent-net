package main

import (
	"log"

	"github.com/niparis/agent-net/internal/toolcatalog"
)

func main() {
	if err := toolcatalog.GeneratePluginTools(); err != nil {
		log.Fatal(err)
	}
}
