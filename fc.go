package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// FC represents a connection to the flight controller, which can
// handle disconnections and reconnections on its on. Use NewFC()
// to initialize an FC and then call FC.StartUpdating().
type FC struct {
	portName     string
	baudRate     int
	msp          *MSP
	stdout       io.Writer
	variant      string
	versionMajor byte
	versionMinor byte
	versionPatch byte
	boardID      string
}

// NewFC returns a new FC using the given port and baud rate. stdout is
// optional and will default to os.Stdout if nil
func NewFC(portName string, baudRate int, stdout io.Writer) (*FC, error) {
	msp, err := NewMSP(portName, baudRate)
	if err != nil {
		return nil, err
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	fc := &FC{
		portName: portName,
		baudRate: baudRate,
		msp:      msp,
		stdout:   stdout,
	}
	fc.updateInfo()
	return fc, nil
}

func (f *FC) reconnect() error {
	for {
		msp, err := NewMSP(f.portName, f.baudRate)
		if err == nil {
			f.printf("Reconnected to %s @ %dbps\n", f.portName, f.baudRate)
			f.reset()
			f.msp = msp
			f.updateInfo()
			return nil
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *FC) updateInfo() {
	// Send commands to print FC info
	f.msp.WriteCmd(mspAPIVersion, nil)
	f.msp.WriteCmd(mspFCVariant, nil)
	f.msp.WriteCmd(mspFCVersion, nil)
	f.msp.WriteCmd(mspBoardInfo, nil)
	f.msp.WriteCmd(mspBuildInfo, nil)
}

func (f *FC) printf(format string, a ...interface{}) (int, error) {
	return fmt.Fprintf(f.stdout, format, a...)
}

func (f *FC) printInfo() {
	if f.variant != "" && f.versionMajor != 0 && f.boardID != "" {
		f.printf("%s %d.%d.%d (board %s)\n", f.variant, f.versionMajor, f.versionMinor, f.versionPatch, f.boardID)
	}
}

func (f *FC) handleFrame(fr *MSPFrame) {
	switch fr.Code {
	case mspAPIVersion:
		f.printf("MSP API version %d.%d (protocol %d)\n", fr.Byte(1), fr.Byte(2), fr.Byte(0))
	case mspFCVariant:
		f.variant = string(fr.Payload)
		f.printInfo()
	case mspFCVersion:
		f.versionMajor = fr.Byte(0)
		f.versionMinor = fr.Byte(1)
		f.versionPatch = fr.Byte(2)
		f.printInfo()
	case mspBoardInfo:
		f.boardID = string(fr.Payload)
		f.printInfo()
	case mspBuildInfo:
		buildDate := string(fr.Payload[:11])
		buildTime := string(fr.Payload[11:19])
		rev := string(fr.Payload[19:27])
		f.printf("Build %s (built on %s @ %s)\n", rev, buildDate, buildTime)
	case mspDebugMsg:
		s := strings.Trim(string(fr.Payload), " \r\n\t\x00")
		f.printf("[DEBUG] %s\n", s)
	case mspReboot:
		f.printf("Rebooting board...\n")
	default:
		f.printf("Unhandled MSP frame %d with payload %v\n", fr.Code, fr.Payload)
	}
}

// Reboot reboots the board via MSP_REBOOT
func (f *FC) Reboot() {
	f.msp.WriteCmd(mspReboot, nil)
}

// StartUpdating starts reading from the MSP port and handling
// the received messages. Note that it never returns.
func (f *FC) StartUpdating() {
	for {
		frame, err := f.msp.ReadFrame()
		if err != nil {
			if err == io.EOF {
				f.printf("Board disconnected, trying to reconnect...\n")
				if err := f.reconnect(); err != nil {
					panic(err)
				}
				continue
			}
			panic(err)
		}
		f.handleFrame(frame)
	}
}

func (f *FC) reset() {
	f.variant = ""
	f.versionMajor = 0
	f.versionMinor = 0
	f.versionPatch = 0
	f.boardID = ""
}
