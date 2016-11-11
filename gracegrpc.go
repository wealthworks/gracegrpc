package gracegrpc

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/facebookgo/grace/gracenet"
	"google.golang.org/grpc"
)

var (
	verbose    = flag.Bool("gracelog", true, "Enable logging.")
	didInherit = os.Getenv("LISTEN_FDS") != ""
	ppid       = os.Getppid()
)

type graceGrpc struct {
	server   *grpc.Server
	net      *gracenet.Net
	listener net.Listener
	errors   chan error
}

func NewGraceGrpc(s *grpc.Server, net, addr string) *graceGrpc {
	gr := &graceGrpc{
		server: s,
		net:    &gracenet.Net{},

		//for  StartProcess error.
		errors: make(chan error),
	}
	l, err := gr.net.Listen(net, addr)
	if err != nil {
		panic(err)
	}
	gr.listener = l
	return gr
}

func (gr *graceGrpc) serve() {
	go gr.server.Serve(gr.listener)
}

func (gr *graceGrpc) wait() {
	var wg sync.WaitGroup
	wg.Add(1)
	go gr.signalHandler(&wg)
	wg.Wait()
}

func (gr *graceGrpc) signalHandler(wg *sync.WaitGroup) {
	ch := make(chan os.Signal, 10)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR2)
	for {
		sig := <-ch
		switch sig {
		case syscall.SIGINT, syscall.SIGTERM:
			signal.Stop(ch)
			gr.server.GracefulStop()
			wg.Done()
			return
		case syscall.SIGUSR2:
			if _, err := gr.net.StartProcess(); err != nil {
				gr.errors <- err
			}
		}
	}
}

func (gr *graceGrpc) Serve() error {

	if *verbose {
		if didInherit {
			if ppid == 1 {
				log.Printf("Listening on init activated %s\n", pprintAddr(gr.listener))
			} else {
				const msg = "Graceful handoff of %s with new pid %d replace old pid %d"
				log.Printf(msg, pprintAddr(gr.listener), os.Getpid(), ppid)
			}
		} else {
			const msg = "Serving %s with pid %d\n"
			log.Printf(msg, pprintAddr(gr.listener), os.Getpid())
		}
	}

	gr.serve()

	if didInherit && ppid != 1 {
		if err := syscall.Kill(ppid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("failed to close parent: %s", err)
		}
	}

	waitdone := make(chan struct{})
	go func() {
		defer close(waitdone)
		gr.wait()
	}()

	select {
	case err := <-gr.errors:
		if err == nil {
			panic("unexpected nil error")
		}
		return err
	case <-waitdone:
		if *verbose {
			log.Printf("Exiting pid %d.", os.Getpid())
		}
		return nil
	}
}

func pprintAddr(l net.Listener) []byte {
	var out bytes.Buffer
	fmt.Fprint(&out, l.Addr())
	return out.Bytes()
}
