package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	sock := flag.String("socket", "", "the socket to listen on: $XDG_RUNTIME_DIR/broadway<N+1>.socket for BROADWAY_DISPLAY=:N")
	width := flag.Uint("width", 1280, "the screen's width")
	height := flag.Uint("height", 800, "the screen's height")
	flag.Parse()
	if *sock == "" || *width == 0 || *height == 0 || *width > 1<<15 || *height > 1<<15 {
		fmt.Fprintln(os.Stderr, "usage: nullbroadway -socket PATH -width W -height H")
		os.Exit(2)
	}
	fatal = func(err error) {
		fmt.Fprintln(os.Stderr, "nullbroadway:", err)
		os.Remove(*sock)
		os.Exit(1)
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: *sock, Net: "unix"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nullbroadway:", err)
		os.Exit(1)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-sig
		l.Close() // removes the socket file
	}()
	if err := newServer(uint32(*width), uint32(*height)).serve(l); err != nil {
		fatal(err)
	}
}
