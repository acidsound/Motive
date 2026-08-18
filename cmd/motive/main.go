package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/acidsound/Motive/internal/tui"
)

func main() {
	tuiMode := flag.Bool("tui", false, "start the terminal UI")
	flag.Parse()

	if *tuiMode || flag.NArg() == 0 {
		if err := tui.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("request: %s\n", flag.Arg(0))
	fmt.Println("model execution is not wired yet")
}
