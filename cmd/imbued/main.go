package main

import (
	"context"
	"flag"
	"imbue/internal/config"
	"imbue/internal/daemon"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	root := flag.String("home", config.DefaultRoot(), "Imbue data directory")
	flag.Parse()
	c, e := config.Load(*root)
	if e != nil {
		log.Fatal(e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e = daemon.Run(ctx, c); e != nil {
		log.Fatal(e)
	}
}
