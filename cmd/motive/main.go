package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/tui"
)

func main() {
	tuiMode := flag.Bool("tui", false, "start the terminal UI")
	flag.Parse()
	rt := runtime.New(model.NewFromEnv())
	if *tuiMode || flag.NArg() == 0 {
		if err := tui.Run(rt); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
		return
	}
	result, err := rt.Execute(context.Background(), flag.Arg(0))
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	fmt.Println(result)
}
