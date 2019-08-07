package fc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/fiam/msp-tool/msp"
	"github.com/fiam/msp-tool/rx"
)

const (
	dfuDevicePrefix     = "Found DFU: "
	internalFlashMarker = "@Internal Flash  /"
)

type PIDReceiver interface {
	ReceivedPID(map[string]*Pid) error
}

type Pid struct {
	FlightSurface string
	Value         []uint8
}

// FC represents a connection to the flight controller, which can
// handle disconnections and reconnections on its on. Use NewFC()
// to initialize an FC and then call FC.StartUpdating().
type FC struct {
	opts         FCOptions
	msp          *msp.MSP
	variant      string
	versionMajor byte
	versionMinor byte
	versionPatch byte
	boardID      string
	targetName   string
	Features     uint32
	channelMap   []uint8
	PidMap       map[string]*Pid
	rxTicker     *time.Ticker
	sticks       rx.RxSticks
}

type FCOptions struct {
	PortName         string
	BaudRate         int
	Stdout           io.Writer
	EnableDebugTrace bool
}

func (f *FCOptions) stderr() io.Writer {
	return f.Stdout
}

// NewFC returns a new FC using the given port and baud rate. stdout is
// optional and will default to os.Stdout if nil
func NewFC(opts FCOptions) (*FC, error) {
	m, err := msp.New(opts.PortName, opts.BaudRate)
	if err != nil {
		return nil, err
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	fc := &FC{
		opts: opts,
		msp:  m,
	}
	fc.reset()
	fc.updateInfo()
	return fc, nil
}

func (f *FC) reconnect() error {
	if f.msp != nil {
		f.msp.Close()
		f.msp = nil
	}
	for {
		// Trying to connect on macOS when the port dev file is
		// not present would cause an USB hub reset.
		if f.portIsPresent() {
			m, err := msp.New(f.opts.PortName, f.opts.BaudRate)
			if err == nil {
				f.printf("Reconnected to %s @ %dbps\n", f.opts.PortName, f.opts.BaudRate)
				f.reset()
				f.msp = m
				f.updateInfo()
				return nil
			}
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *FC) Close() error {
	if f.msp != nil {
		err := f.msp.Close()
		f.msp = nil
		if err != nil {
			return err
		}
	}

	return nil
}

func (f *FC) updateInfo() {
	// Send commands to print FC info
	f.msp.WriteCmd(msp.MspAPIVersion)
	f.msp.WriteCmd(msp.MspFCVariant)
	f.msp.WriteCmd(msp.MspFCVersion)
	f.msp.WriteCmd(msp.MspBoardInfo)
	f.msp.WriteCmd(msp.MspBuildInfo)
	f.msp.WriteCmd(msp.MspFeature)
	f.msp.WriteCmd(msp.MspCFSerialConfig)
	f.msp.WriteCmd(msp.MspRXMap)
}

func (f *FC) printf(format string, a ...interface{}) (int, error) {
	return fmt.Fprintf(f.opts.Stdout, format, a...)
}

func (f *FC) printInfo() {
	if f.variant != "" && f.versionMajor != 0 && f.boardID != "" {
		targetName := ""
		if f.targetName != "" {
			targetName = ", target " + f.targetName
		}
		f.printf("%s %d.%d.%d (board %s%s)\n", f.variant, f.versionMajor, f.versionMinor, f.versionPatch, f.boardID, targetName)
	}
}

func (f *FC) handleFrame(fr *msp.MSPFrame, w interface{}) error {
	switch fr.Code {
	case msp.MspAPIVersion:
		f.printf("MSP API version %d.%d (protocol %d)\n", fr.Byte(1), fr.Byte(2), fr.Byte(0))
	case msp.MspFCVariant:
		f.variant = string(fr.Payload)
		f.printInfo()
	case msp.MspFCVersion:
		f.versionMajor = fr.Byte(0)
		f.versionMinor = fr.Byte(1)
		f.versionPatch = fr.Byte(2)
		f.printInfo()
	case msp.MspBoardInfo:
		// BoardID is always 4 characters
		f.boardID = string(fr.Payload[:4])
		// Then 4 bytes follow, HW revision (uint16), builtin OSD type (uint8) and wether
		// the board uses VCP (uint8), We ignore those bytes here. Finally, in recent BF
		// and iNAV versions, the length of the targetName (uint8) followed by the target
		// name itself is sent. Try to retrieve it.
		if len(fr.Payload) >= 9 {
			targetNameLength := int(fr.Payload[8])
			if len(fr.Payload) > 8+targetNameLength {
				f.targetName = string(fr.Payload[9 : 9+targetNameLength])
			}
		}
		f.printInfo()
	case msp.MspBuildInfo:
		buildDate := string(fr.Payload[:11])
		buildTime := string(fr.Payload[11:19])
		// XXX: Revision is 8 characters in iNav but 7 in BF/CF
		rev := string(fr.Payload[19:])
		f.printf("Build %s (built on %s @ %s)\n", rev, buildDate, buildTime)
	case msp.MspFeature:
		fr.Read(&f.Features)
		if (f.Features&msp.MspFCFeatureDebugTrace == 0) && f.shouldEnableDebugTrace() {
			f.printf("Enabling FEATURE_DEBUG_TRACE\n")
			f.Features |= msp.MspFCFeatureDebugTrace
			f.msp.WriteCmd(msp.MspSetFeature, f.Features)
			f.msp.WriteCmd(msp.MspEepromWrite)
		}
	case msp.MspCFSerialConfig:
		if f.shouldEnableDebugTrace() {
			var cfg msp.MSPSerialConfig
			var serialConfigs []msp.MSPSerialConfig
			hasDebugTraceMSPPort := false
			mask := uint16(msp.SerialFunctionMSP | msp.SerialFunctionDebugTrace)
			for {
				err := fr.Read(&cfg)
				if err != nil {
					if err == io.EOF {
						// All ports read
						break
					}
					panic(err)
				}
				if cfg.FunctionMask&mask == mask {
					hasDebugTraceMSPPort = true
				}
				serialConfigs = append(serialConfigs, cfg)
			}
			if !hasDebugTraceMSPPort {
				// Enable DEBUG_TRACE on the first MSP port, since DEBUG_TRACE only
				// works on one port.
				for ii := range serialConfigs {
					if serialConfigs[ii].FunctionMask&msp.SerialFunctionMSP != 0 {
						f.printf("Enabling FUNCTION_DEBUG_TRACE on serial port %v\n", serialConfigs[ii].Identifier)
						serialConfigs[ii].FunctionMask |= msp.SerialFunctionDebugTrace
						break
					}
				}
				// Save ports
				f.msp.WriteCmd(msp.MspSetCFSerialConfig, serialConfigs)
				f.msp.WriteCmd(msp.MspEepromWrite)
			}
		}
	case msp.MspRXMap:
		f.channelMap = make([]uint8, 8)
		if err := fr.Read(f.channelMap); err != nil {
			return err
		}
	case msp.MspReboot:
		f.printf("Rebooting board...\n")
	case msp.MspDebugMsg:
		s := strings.Trim(string(fr.Payload), " \r\n\t\x00")
		f.printf("[DEBUG] %s\n", s)
	case msp.MspSetFeature:
	case msp.MspSetCFSerialConfig:
	case msp.MspSetRawRC:
	case msp.MspEepromWrite:
	case msp.MspSetPID:
		// Nothing to do for these
	case msp.MspPID:
		pidMap := make([]uint8, 30)
		if err := fr.Read(pidMap); err != nil {
			return err
		}

		rollPid := &Pid{"roll", pidMap[0:3]}
		pitchPid := &Pid{"pitch", pidMap[3:6]}
		yawPid := &Pid{"pitch", pidMap[6:8]}
		altPid := &Pid{"alt", pidMap[8:11]}
		velPid := &Pid{"vel", pidMap[11:14]}
		magPid := &Pid{"mag", pidMap[14:15]}
		posPid := &Pid{"pos", pidMap[15:16]}
		posRPid := &Pid{"posR", pidMap[16:19]}
		navRPid := &Pid{"navR", pidMap[19:22]}

		f.PidMap = map[string]*Pid{
			"roll":  rollPid,
			"pitch": pitchPid,
			"yaw":   yawPid,
			"alt":   altPid,
			"vel":   velPid,
			"mag":   magPid,
			"pos":   posPid,
			"posR":  posRPid,
			"navR":  navRPid,
		}

		if pw, ok := w.(PIDReceiver); ok {
			pw.ReceivedPID(f.PidMap)
			return nil
		}
	default:
		f.printf("Unhandled MSP frame %d with payload %v\n", fr.Code, fr.Payload)
	}
	return nil
}

func (f *FC) versionGte(major, minor, patch byte) bool {
	return f.versionMajor > major || (f.versionMajor == major && f.versionMinor > minor) ||
		(f.versionMajor == major && f.versionMinor == minor && f.versionPatch >= patch)
}

func (f *FC) shouldEnableDebugTrace() bool {
	// Only INAV 1.9+ supports DEBUG_TRACE for now
	return f.opts.EnableDebugTrace && f.variant == "INAV" && f.versionGte(1, 9, 0)
}

func (f *FC) prepareToReboot(fn func(m *msp.MSP) error) error {
	// We want to avoid an EOF from the uart at all costs,
	// so close the current port and open another one to ensure
	// the goroutine reading from the port stops even if the
	// board reboots very fast.
	m := f.msp
	f.msp = nil
	m.Close()
	time.Sleep(time.Second)
	mm, err := msp.New(f.opts.PortName, f.opts.BaudRate)
	if err != nil {
		return err
	}
	err = fn(mm)
	mm.Close()
	return err
}

// Reboot reboots the board via MSP_REBOOT
func (f *FC) Reboot() {
	f.prepareToReboot(func(m *msp.MSP) error {
		m.WriteCmd(msp.MspReboot)
		return nil
	})
}

func (f *FC) unwrapError(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}

func (f *FC) portIsPresent() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	_, err := os.Stat(f.opts.PortName)
	return err == nil
}

// StartUpdating starts reading from the MSP port and handling
// the received messages. Note that it never returns.
func (f *FC) StartUpdating(w interface{}) {
	for {
		var frame *msp.MSPFrame
		var err error
		m := f.msp
		if m != nil {
			frame, err = m.ReadFrame()
		} else {
			// f.msp was intentionally set to nil because the board
			// was rebooted. Assume a disconnection. Note that we can't
			// rely just on EOF detection because in some cases
			// (e.g. macOS with STM32 VCP uart) reading from the uart
			// until EOF will cause a USB reset, affecting other devices
			// connected to the same hub. Assign err to os.ErrClosed
			// to apply the same logic for port detection than the
			// path that handles a closed port.
			err = os.ErrClosed
		}
		if err != nil {
			if merr, ok := err.(msp.MSPError); ok && merr.IsMSPError() {
				f.printf("%v\n", err)
				continue
			}
			uerr := f.unwrapError(err)
			f.printf("Board disconnected (%v), trying to reconnect...\n", uerr)
			if uerr == os.ErrClosed {
				time.Sleep(time.Second)
				// Wait for the port to go away or a 5s timeout
				timeout := time.Now().Add(5 * time.Second)
				for f.portIsPresent() {
					if timeout.Before(time.Now()) {
						break
					}
				}
			}
			if err := f.reconnect(); err != nil {
				panic(err)
			}
			f.printf("Reconnected...\n")
			continue
		}
		f.handleFrame(frame, w)
	}
}

// HasDetectedTargetName returns true iff the target name installed on
// the board has been retrieved via MSP.
func (f *FC) HasDetectedTargetName() bool {
	return f.targetName != ""
}

// Flash compiles the given target and flashes the board
func (f *FC) Flash(srcDir string, targetName string) error {
	if targetName == "" {
		targetName = f.targetName

		if targetName == "" {
			return errors.New("empty target name")
		}
	}
	// First, check that dfu-util is available
	dfu, err := exec.LookPath("dfu-util")
	if err != nil {
		return err
	}
	// Now compile the target
	cmd := exec.Command("make", "binary")
	cmd.Stdout = f.opts.Stdout
	cmd.Stderr = f.opts.stderr()
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

func (f *FC) IsSimulatingRX() bool {
	return f.rxTicker != nil
}

func (f *FC) ToggleRXSimulation() (enabled bool, err error) {
	if f.rxTicker != nil {
		f.rxTicker.Stop()
		f.rxTicker = nil
	} else {
		f.rxTicker = time.NewTicker(10 * time.Millisecond)
		go func(t *time.Ticker) {
			for range t.C {
				f.sticks.Update()
				m := f.msp
				if m == nil {
					continue
				}
				m.WriteCmd(msp.MspSetRawRC, f.sticks.ToMSP(f.channelMap))
			}
		}(f.rxTicker)
		enabled = true
	}
	return enabled, err
}

func (f *FC) GetPIDs() (err error) {
	f.msp.WriteCmd(msp.MspPID)

	return err
}

func (f *FC) SetPIDs(pids []uint8) (err error) {
	f.msp.WriteCmd(msp.MspSetPID, pids)
	f.msp.WriteCmd(msp.MspEepromWrite)

	return err
}

func (f *FC) RX() rx.RX {
	return &f.sticks
}

// Reboots the board into the bootloader for flashing
func (f *FC) dfuReboot() error {
	return f.prepareToReboot(func(m *msp.MSP) error {
		_, err := m.RebootIntoBootloader()
		return err
	})
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
	cmd.Stdout = f.opts.Stdout
	cmd.Stderr = f.opts.stderr()
	return cmd.Run()
}

func (f *FC) reset() {
	f.variant = ""
	f.versionMajor = 0
	f.versionMinor = 0
	f.versionPatch = 0
	f.boardID = ""
	f.targetName = ""
	f.Features = 0
	f.channelMap = nil
	if f.rxTicker != nil {
		f.rxTicker.Stop()
		f.rxTicker = nil
	}
	f.sticks = rx.RxSticks{
		Roll:     rx.RxMid,
		Pitch:    rx.RxMid,
		Yaw:      rx.RxMid,
		Throttle: rx.RxMid,
	}
}
