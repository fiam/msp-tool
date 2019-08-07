package rx

import (
	"sync"
	"time"
)

const (
	RxLow  = 1000
	RxMid  = 1500
	RxHigh = 2000
)

const (
	keyTimeout = 100 * time.Millisecond
)

type RXKey uint8

// RX keys. WASD controls left stick while
// the arrows control the right one. Numbers
// 1-0 control channels 5-14
const (
	RXKeyW RXKey = iota
	RXKeyA
	RXKeyS
	RXKeyD
	RXKeyUp
	RXKeyLeft
	RXKeyDown
	RXKeyRight

	RXKey1
	RXKey2
	RXKey3
	RXKey4
	RXKey5
	RXKey6
	RXKey7
	RXKey8
	RXKey9
	RXKey0
)

const rxKeyCount = RXKey0 + 1

type RX interface {
	Keypress(key RXKey)
}

type RxSticks struct {
	Roll      uint16
	Pitch     uint16
	Yaw       uint16
	Throttle  uint16
	Channels  [14]uint16 // Channels 5-18
	mu        sync.Mutex
	lastPress [rxKeyCount]time.Time
}

func (r *RxSticks) Reset() {
	r.Roll = RxMid
	r.Pitch = RxMid
	r.Yaw = RxMid
	r.Throttle = RxLow
	for ii := range r.Channels {
		r.Channels[ii] = RxLow
	}
}

func (r *RxSticks) ToMSP(channelMap []uint8) rxPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	channels := make([]uint16, 4)
	channels[channelMap[0]] = r.Roll
	channels[channelMap[1]] = r.Pitch
	channels[channelMap[2]] = r.Yaw
	channels[channelMap[3]] = r.Throttle
	channels = append(channels, r.Channels[:]...)
	return rxPayload{
		Channels: channels,
	}
}

func (r *RxSticks) Keypress(key RXKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch key {
	case RXKeyW:
		r.Throttle = RxHigh
		r.lastPress[RXKeyS] = time.Time{}
	case RXKeyA:
		r.Yaw = RxLow
		r.lastPress[RXKeyD] = time.Time{}
	case RXKeyS:
		r.Throttle = RxLow
		r.lastPress[RXKeyW] = time.Time{}
	case RXKeyD:
		r.Yaw = RxHigh
		r.lastPress[RXKeyA] = time.Time{}
	case RXKeyUp:
		r.Pitch = RxHigh
		r.lastPress[RXKeyDown] = time.Time{}
	case RXKeyLeft:
		r.Roll = RxLow
		r.lastPress[RXKeyRight] = time.Time{}
	case RXKeyDown:
		r.Pitch = RxLow
		r.lastPress[RXKeyUp] = time.Time{}
	case RXKeyRight:
		r.Roll = RxHigh
		r.lastPress[RXKeyRight] = time.Time{}
	case RXKey1:
		r.switchChannel(5)
	case RXKey2:
		r.switchChannel(6)
	case RXKey3:
		r.switchChannel(7)
	case RXKey4:
		r.switchChannel(8)
	case RXKey5:
		r.switchChannel(9)
	case RXKey6:
		r.switchChannel(10)
	case RXKey7:
		r.switchChannel(11)
	case RXKey8:
		r.switchChannel(12)
	case RXKey9:
		r.switchChannel(13)
	case RXKey0:
		r.switchChannel(14)

	}
	r.lastPress[key] = time.Now()
}

func (r *RxSticks) Update() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for ii, ts := range r.lastPress {
		if ts.Equal(time.Time{}) {
			continue
		}
		if ts.Add(keyTimeout).Before(now) {
			r.lastPress[ii] = time.Time{}
			switch RXKey(ii) {
			case RXKeyW, RXKeyS:
				r.Throttle = RxMid
			case RXKeyA, RXKeyD:
				r.Yaw = RxMid
			case RXKeyUp, RXKeyDown:
				r.Pitch = RxMid
			case RXKeyLeft, RXKeyRight:
				r.Roll = RxMid
			}
		}
	}
}

func (r *RxSticks) switchChannel(ch int) {
	idx := ch - 5
	if idx >= 0 && idx < len(r.Channels) {
		if r.Channels[idx] == RxLow {
			r.Channels[idx] = RxHigh
		} else {
			r.Channels[idx] = RxLow
		}
	}
}

type rxPayload struct {
	Channels []uint16
}
