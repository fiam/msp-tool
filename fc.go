package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	dfuDevicePrefix     = "Found DFU: "
	internalFlashMarker = "@Internal Flash  /"
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

// Flash compiles the given target and flashes the board
func (f *FC) Flash(srcDir string, targetName string) error {
	// First, check that dfu-util is available
	dfu, err := exec.LookPath("dfu-util")
	if err != nil {
		return err
	}
	// Now compile the target
	cmd := exec.Command("make", "binary")
	cmd.Stdout = f.stdout
	cmd.Stderr = f.stdout
	cmd.Stdin = os.Stdin
	var env []string
	env = append(env, os.Environ()...)
	env = append(env, "TARGET="+targetName)
	cmd.Env = env
	cmd.Dir = srcDir

	f.printf("Building binary for %s...\n", targetName)

	if err := cmd.Run(); err != nil {
		return err
	}

	// Check existing .bin files in the output directory
	obj := filepath.Join(srcDir, "obj")
	files, err := ioutil.ReadDir(obj)
	if err != nil {
		return err
	}

	var binary os.FileInfo

	for _, f := range files {
		name := f.Name()
		if filepath.Ext(name) == ".bin" {
			nonExt := name[:len(name)-4]
			// Binaries end with the target name
			if strings.HasSuffix(nonExt, targetName) {
				if binary == nil || binary.ModTime().Before(f.ModTime()) {
					binary = f
				}
			}
		}
	}
	if binary == nil {
		return fmt.Errorf("could not find binary for target %s", targetName)
	}

	binaryPath := filepath.Join(obj, binary.Name())

	f.printf("Rebooting board in DFU mode...\n")

	// Now reboot in dfu mode
	if err := f.dfuReboot(); err != nil {
		return err
	}
	if err := f.dfuWait(dfu); err != nil {
		return err
	}
	return f.dfuFlash(dfu, binaryPath)
}

// Reboots the board into the bootloader for flashing
func (f *FC) dfuReboot() error {
	_, err := f.msp.RebootIntoBootloader()
	return err
}

func (f *FC) dfuList(dfuPath string) ([]string, error) {
	cmd := exec.Command(dfuPath, "--list")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Run()
	lines := strings.Split(buf.String(), "\n")
	var dfuLines []string
	for _, ll := range lines {
		ll = strings.Trim(ll, "\n\r\t ")
		if strings.HasPrefix(ll, dfuDevicePrefix) {
			dfuLines = append(dfuLines, ll[len(dfuDevicePrefix):])
		}
	}
	return dfuLines, nil
}

func (f *FC) dfuWait(dfuPath string) error {
	timeout := time.Now().Add(30 * time.Second)
	for {
		if timeout.Before(time.Now()) {
			return fmt.Errorf("timed out while waiting for board in DFU mode")
		}
		devices, err := f.dfuList(dfuPath)
		if err != nil {
			return err
		}
		for _, dev := range devices {
			if strings.Contains(dev, internalFlashMarker) {
				// Found a flash device
				return nil
			}
		}
	}
}

func (f *FC) regexpFind(pattern string, s string) string {
	r := regexp.MustCompile(pattern)
	m := r.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func (f *FC) dfuFlash(dfuPath string, binaryPath string) error {
	devices, err := f.dfuList(dfuPath)
	if err != nil {
		return err
	}
	var device string
	for _, dev := range devices {
		if strings.Contains(dev, internalFlashMarker) {
			device = dev
			break
		}
	}
	// a device line looks like:
	// [0483:df11] ver=2200, devnum=17, cfg=1, intf=0, path="20-1", alt=0, name="@Internal Flash  /0x08000000/04*016Kg,01*064Kg,07*128Kg", serial="3276365D3336"
	// We need to extract alt, serial and the flash offset
	alt := f.regexpFind("alt=(\\d+)", device)
	serial := f.regexpFind(`serial="(.*?)"`, device)
	offset := f.regexpFind("Internal Flash  /([\\dx]*?)/", device)
	if alt == "" || serial == "" || offset == "" {
		return fmt.Errorf("could not determine flash parameters from %q", device)
	}
	f.printf("Flashing %s via DFU to offset %s...\n", filepath.Base(binaryPath), offset)
	cmd := exec.Command(dfuPath, "-a", alt, "-S", serial, "-s", offset+":leave", "-D", binaryPath)
	cmd.Stdout = f.stdout
	cmd.Stderr = f.stdout
	return cmd.Run()
}

func (f *FC) reset() {
	f.variant = ""
	f.versionMajor = 0
	f.versionMinor = 0
	f.versionPatch = 0
	f.boardID = ""
}
