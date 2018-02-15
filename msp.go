package main

import (
	"bytes"
	"fmt"
	"io"

	"github.com/tarm/serial"
)

const (
	mspAPIVersion = 1
	mspFCVariant  = 2
	mspFCVersion  = 3
	mspBoardInfo  = 4
	mspBuildInfo  = 5

	mspReboot = 68

	mspDebugMsg = 253
)

func mspV1Encode(cmd byte, data []byte) []byte {
	var payloadLength byte
	if len(data) > 0 {
		payloadLength = byte(len(data))
	}
	var buf bytes.Buffer
	buf.WriteByte('$')
	buf.WriteByte('M')
	buf.WriteByte('<')
	buf.WriteByte(payloadLength)
	buf.WriteByte(cmd)
	if payloadLength > 0 {
		buf.Write(data)
	}
	crc := byte(0)
	for _, v := range buf.Bytes()[3:] {
		crc ^= v
	}
	buf.WriteByte(crc)
	return buf.Bytes()
}

func mspV2Encode(cmd byte, totalLength int) []byte {
	var payloadLength byte
	if totalLength > 6 {
		payloadLength = byte(totalLength) - 9
	}
	var buf bytes.Buffer
	buf.WriteByte('$')
	buf.WriteByte('X')
	buf.WriteByte('<')
	buf.WriteByte(0)
	buf.WriteByte(cmd)
	buf.WriteByte(0)
	buf.WriteByte(byte(payloadLength))
	buf.WriteByte(0)
	for ii := byte(0); ii < payloadLength; ii++ {
		buf.WriteByte(0)
	}
	crc := byte(0)
	for _, v := range buf.Bytes()[3:] {
		crc = crc8_dvb_s2(crc, v)
	}
	buf.WriteByte(crc)
	return buf.Bytes()
}

func crc8_dvb_s2(crc, a byte) byte {
	crc ^= a
	for ii := 0; ii < 8; ii++ {
		if (crc & 0x80) != 0 {
			crc = (crc << 1) ^ 0xD5
		} else {
			crc = crc << 1
		}
	}
	return crc
}

type MSP struct {
	portName string
	baudRate int
	port     *serial.Port
}

type MSPFrame struct {
	Code    uint16
	Payload []byte
}

func (f *MSPFrame) Byte(idx int) byte {
	return f.Payload[idx]
}

func NewMSP(portName string, baudRate int) (*MSP, error) {
	opts := &serial.Config{
		Name: portName,
		Baud: baudRate,
	}
	port, err := serial.OpenPort(opts)
	if err != nil {
		return nil, err
	}
	return &MSP{
		portName: portName,
		baudRate: baudRate,
		port:     port,
	}, nil
}

func (m *MSP) WriteCmd(cmd uint16, data []byte) (int, error) {
	frame := mspV1Encode(byte(cmd), data)
	return m.port.Write(frame)
}

func (m *MSP) readMSPV1Frame() (*MSPFrame, error) {
	buf := make([]byte, 3)
	if _, err := m.port.Read(buf); err != nil {
		return nil, err
	}
	if buf[0] != '<' && buf[0] != '>' {
		return nil, fmt.Errorf("invalid MSP direction char 0x%02x", buf[0])
	}
	var payload []byte
	payloadLength := int(buf[1])
	if payloadLength > 0 {
		payload = make([]byte, payloadLength)
		if _, err := io.ReadFull(m.port, payload); err != nil {
			return nil, err
		}
	}
	cmd := buf[2]
	buf = buf[:1]
	if _, err := m.port.Read(buf); err != nil {
		return nil, err
	}
	// crc := buf[0]
	return &MSPFrame{
		Code:    uint16(cmd),
		Payload: payload,
	}, nil
}

func (m *MSP) readMSPV2Frame() (*MSPFrame, error) {
	buf := make([]byte, 6)
	if _, err := m.port.Read(buf); err != nil {
		return nil, err
	}
	if buf[0] != '<' && buf[0] != '>' {
		return nil, fmt.Errorf("invalid MSP direction char 0x%02x", buf[0])
	}
	// flags := buf[1]
	code := uint16(buf[2]) | uint16(buf[3])<<8
	payloadLength := int(uint16(buf[4]) | uint16(buf[5])<<8)
	var payload []byte
	if payloadLength > 0 {
		payload = make([]byte, payloadLength)
		if _, err := io.ReadFull(m.port, payload); err != nil {
			return nil, err
		}
	}

	buf = make([]byte, 1)
	if _, err := m.port.Read(buf); err != nil {
		return nil, err
	}
	// crc := buf[0]
	return &MSPFrame{
		Code:    code,
		Payload: payload,
	}, nil
}

func (m *MSP) ReadFrame() (*MSPFrame, error) {
	buf := make([]byte, 1)
	for {
		_, err := m.port.Read(buf)
		if err != nil {
			return nil, err
		}
		if buf[0] == '$' {
			// Frame start
			break
		}
	}
	_, err := m.port.Read(buf)
	if err != nil {
		return nil, err
	}
	switch buf[0] {
	case 'M':
		return m.readMSPV1Frame()
	case 'X':
		return m.readMSPV2Frame()
	default:
		return nil, fmt.Errorf("unknown MSP char %c", buf[0])
	}
}

/*
func main() {
	opts := &serial.Config{
		Name:        os.Args[1],
		Baud:        115200,
		ReadTimeout: time.Second * 3,
	}
	port, err := serial.OpenPort(opts)
	if err != nil {
		log.Fatalf("serial.Open: %v", err)
	}
	defer port.Close()

	encode := mspV1Encode
	expectedRespLength := 6
	if len(os.Args) > 2 && os.Args[2] == "2" {
		encode = mspV2Encode
		expectedRespLength = 9
		fmt.Println("using MSPv2")
	} else {
		fmt.Println("using MSPv1")
	}
	v := expectedRespLength
	for v < 66 {
		//cmd := byte(1) // MSP_API_VERSION
		cmd := byte(87) // MSP_OSD_CHAR_WRITE
		//if v != 0 {
		//	cmd = byte(87) // MSP_OSD_CHAR_WRITE
		//}
		data := encode(cmd, v)
		fmt.Printf("will send %d bytes: %v\n", len(data), data)
		n, err := port.Write(data)
		if err != nil {
			fmt.Println(err)
			continue
		}
		if err := port.Flush(); err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Printf("did send %d bytes\n", n)
		b := make([]byte, expectedRespLength)
		nn := 0
		time.Sleep(100 * time.Millisecond)
		for jj := 0; jj < 3 && nn < len(b); jj++ {
			nn = 0
			for nn < len(b) {
				n, err := port.Read(b[nn:])
				if err != nil {
					fmt.Println(b)
					fmt.Println(err)
					break
				}
				nn += n
			}
		}
		if nn < len(b) {
			continue
		}
		fmt.Printf("got response %d bytes: %v\n", nn, b)
		v++
	}
}*/
