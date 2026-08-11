package main

import (
	"fmt"
	"os"
)

func main() {
	application, err := NewPeerApplication(os.Args, os.Stdin, os.Stdout)
	if err == nil {
		err = application.Run()
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "released compatibility peer:", err)
		os.Exit(1)
	}
}
