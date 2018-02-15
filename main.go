package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"syscall"

	"github.com/pkg/term"
)

var (
	portName = flag.String("p", "", "Serial port")
	baudRate = flag.Int("b", 115200, "Baud rate")

	inputSigInt = byte(3) // ctrl+c
)

type keyboardMonitor struct {
	t  *term.Term
	mu sync.Mutex
}

func (km *keyboardMonitor) Open() error {
	km.mu.Lock()
	defer km.mu.Unlock()
	if km.t != nil {
		return errors.New("already open")
	}
	t, err := term.Open("/dev/tty")
	if err != nil {
		return err
	}
	if err := t.SetRaw(); err != nil {
		return err
	}
	km.t = t
	return nil
}

func (km *keyboardMonitor) Get() (byte, error) {
	km.mu.Lock()
	t := km.t
	km.mu.Unlock()
	if t != nil {
		buf := make([]byte, 3)
		if _, err := t.Read(buf); err != nil {
			return 0, err
		}
		return buf[0], nil
	}
	return 0, nil
}

func (km *keyboardMonitor) Close() error {
	km.mu.Lock()
	defer km.mu.Unlock()
	if km.t != nil {
		if err := km.t.Restore(); err != nil {
			return err
		}
		km.t = nil
	}
	return nil
}

func (km *keyboardMonitor) Write(p []byte) (int, error) {
	if err := km.Close(); err != nil {
		panic(err)
	}
	n, err := os.Stdout.Write(p)
	if err := km.Open(); err != nil {
		panic(err)
	}
	return n, err
}

func printHelp(w io.Writer) {
	help := `
Available commands:
h	Print this help
r	Reboot the board
q	Quit

`
	fmt.Fprint(w, help)
}

func main() {
	flag.Parse()

	if *portName == "" {
		fmt.Fprintf(os.Stderr, "Missing port\n")
		return
	}

	km := &keyboardMonitor{}
	if err := km.Open(); err != nil {
		log.Fatal(err)
	}

	defer km.Close()

	fc, err := NewFC(*portName, *baudRate, km)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Fprintf(km, "Connected to %s @ %dbps. Press 'h' for help.\n", *portName, *baudRate)

	go func() {
		defer km.Close()
		fc.StartUpdating()
	}()
	input := make(chan byte)
	go func() {
		for {
			k, err := km.Get()
			if err == nil {
				input <- k
			}
		}
	}()
	/*	mspFrames := make(chan *mspFrame)
		go func() {
			for {
				frame, err := m.ReadFrame()
				if err == nil {
					mspFrames <- frame
				} else {
					//panic(err)
				}
			}
		}()
	*/
	// main loop
	loop := func() {
		for {
			select {
			case k := <-input:
				switch k {
				case inputSigInt:
					km.Close()
					syscall.Kill(syscall.Getpid(), syscall.SIGINT)
				case 'r':
					// Reboot the board
					fc.Reboot()
				case 'q':
					// Quit
					return
				case 'h':
					printHelp(km)
				}
				/*case frame := <-mspFrames:
				// Close the keyboard monitor while handling
				// a frame, since it might require printing
				// to the terminal
				withCleanTerminal(func() {
					handleFrame(frame)
				})
				*/
			}
		}
	}

	loop()
}
