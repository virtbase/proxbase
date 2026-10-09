package main

import (
	"os"

	"github.com/virtbase/proxbase/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
