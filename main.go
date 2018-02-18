package main

import (
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
	portName              = flag.String("p", "", "Serial port")
	baudRate              = flag.Int("b", 115200, "Baud rate")
	sourceDir             = flag.String("s", ".", "Path to the directory with the firmware source code")
	targetName            = flag.String("t", "", "Target name. Optional if the firmware reports it via MSP")
	doNotEnableDebugTrace = flag.Bool("no-debug-trace", false, "Do not enable DEBUG_TRACE automatically")

	inputSigInt = byte(3) // ctrl+c
)

const (
	kmArrowLeft  = 252
	kmArrowRight = 253
	kmArrowDown  = 254
	kmArrowUp    = 255
)

type keyboardMonitor struct {
	t     *term.Term
	isRaw bool
	mu    sync.Mutex
}

func (km *keyboardMonitor) Open() error {
	km.mu.Lock()
	defer km.mu.Unlock()
	if km.t == nil {
		t, err := term.Open("/dev/tty")
		if err != nil {
			return err
		}
		km.t = t
	}
	if err := km.t.SetRaw(); err != nil {
		return err
	}
	km.isRaw = true
	return nil
}

func (km *keyboardMonitor) Get() (byte, error) {
	km.mu.Lock()
	t := km.t
	isRaw := km.isRaw
	km.mu.Unlock()
	if t != nil && isRaw {
		buf := make([]byte, 3)
		n, err := t.Read(buf)
		if err != nil {
			return 0, err
		}
		if n == 3 && buf[0] == 27 && buf[1] == 91 {
			// Arrow key
			return 255 - (buf[2] - 65), nil
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
		km.isRaw = false
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
f	Build the firmware and flash the board
r	Reboot the board
R	Toggle RX simulation
q	Quit

`
	fmt.Fprint(w, help)
}

func handleRXSimulation(fc *FC, key byte) bool {
	var rxKey RXKey
	switch key {
	case 'w':
		rxKey = RXKeyW
	case 'a':
		rxKey = RXKeyA
	case 's':
		rxKey = RXKeyS
	case 'd':
		rxKey = RXKeyD
	case kmArrowUp:
		rxKey = RXKeyUp
	case kmArrowLeft:
		rxKey = RXKeyLeft
	case kmArrowDown:
		rxKey = RXKeyDown
	case kmArrowRight:
		rxKey = RXKeyRight
	default:
		return false
	}
	fc.RX().Keypress(rxKey)
	return true
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

	opts := FCOptions{
		PortName:         *portName,
		BaudRate:         *baudRate,
		Stdout:           km,
		EnableDebugTrace: !*doNotEnableDebugTrace,
	}
	fc, err := NewFC(opts)
	if err != nil {
		km.Close()
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
	// main loop
	loop := func() {
		for {
			select {
			case k := <-input:
				if fc.IsSimulatingRX() && handleRXSimulation(fc, k) {
					break
				}
				switch k {
				case inputSigInt:
					km.Close()
					syscall.Kill(syscall.Getpid(), syscall.SIGINT)
				case 'h':
					printHelp(km)
				case 'f':
					if *targetName == "" && !fc.HasDetectedTargetName() {
						fmt.Fprintf(km, "missing target name, specify one with -t\n")
						break
					}
					if err := fc.Flash(*sourceDir, *targetName); err != nil {
						fmt.Fprintf(km, "Error flashing board: %v\n", err)
					}
				case 'r':
					// Reboot the board
					fc.Reboot()
				case 'R':
					enabled, err := fc.ToggleRXSimulation()
					if err != nil {
						log.Fatal(err)
					}
					if enabled {
						fmt.Fprintf(km, "Starting RX simulation. Use WASD and arrow keys to control sticks. Press R again to disable.\n")
					} else {
						fmt.Fprintf(km, "Stopping RX simulation\n")
					}
				case 'q':
					// Quit
					return
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
