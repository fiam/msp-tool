# msp-tool

msp-tool is a command line tool for streamlining the 
write/compile/flash/debug cycle in MSP based flight controller firmwares.

It's regularly tested with both [iNAV](https://github.com/inavFlight/inav)
and [Betaflight](https://github.com/betaflight/betaflight), but should work
with any other MSP based firwmare.

## Installation from source

msp-tool is under development, so there are no binary releases at this time.
Instead, developers should install it from its source.

To do so, install [Go](http://golang.org) in your system, then type the
following command:

```sh
  $ go get -v github.com/fiam/msp-tool
```

msp-tool will be installed to ${GOPATH}/bin.

## Using msp-tool

To start msp-tool, the only required argument is `-p`, which indicates the
serial port it should open to connect to the flight controller. Once connected,
it will print some information about the firmware and the board. For example:

```
$ msp-tool -p /dev/tty.usbmodem14211 

Connected to /dev/tty.usbmodem14211 @ 115200bps. Press 'h' for help.
MSP API version 2.1 (protocol 0)
INAV 1.9.0 (board OBSD, target OMNIBUSF4PRO)
Build 2bcdc237 (built on Feb 16 2018 @ 23:16:49)
[DEBUG] [     4.794] Barometer calibration complete (1931)
[DEBUG] [     5.413] Gyro calibration complete (-37, 23, -46)
```

msp-tool will automatically enable `DEBUG_TRACE` output from the FC and
print all output to the terminal. Additionally, it supports keyboard shortcuts
for the following functions:

- **h:** Print the help with all the supported commands
- **q:** Quit msp-tool
- **r:** Reboot the board
- **f:** Compile the firmware for the board, flash it and reboot (see **Flashing** below).

## Flashing
msp-tool allows quickly rebuilding the firmware and flashing it to the board with
a single keystroke via the `f` shortcut. To do so, you need to tell msp-tool a couple
more things via command line arguments:

- The root directory of the firmware source code, via the `-s` command line option.
Its default value is `.` (the current directory), so it doesn't need to be provided
if you're starting msp-tool from the same directory where the Makefile for the firmware
is located.
- The target name, via the `-t` command line option. Note that this is not needed
in INAV 1.9+ and Betaflight 3.4+, since MSP_BOARD_INFO includes the target name and
msp-tool automatically detects it.

For example:

```sh
  $ msp-tool -p /dev/tty.usbmodem14211 -s ~/src/inav -t OMNIBUSF4PRO
```

Note that flashing requires `dfu-util` to be present in your $PATH, since it's used to
actually download the firmware into the flight controller.

## Additional command line options
Call msp-tool with the `-h` argument to print a list of all the available
command line options.


## Getting in touch
I'm usually idling in both INAV's and Betaflights's Slack rooms as **alberto**. Feel free to
send me a message if you'd like to discuss new features.
