package main

import (
	"os"

	"github.com/arthurkushman/subcoder/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
