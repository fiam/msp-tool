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
// the arrows control the right one
const (
	RXKeyW RXKey = iota
	RXKeyA
	RXKeyS
	RXKeyD
	RXKeyUp
	RXKeyLeft
	RXKeyDown
	RXKeyRight
)

type RX interface {
	Keypress(key RXKey)
}

type RxSticks struct {
	Roll      uint16
	Pitch     uint16
	Yaw       uint16
	Throttle  uint16
	mu        sync.Mutex
	lastPress [8]time.Time
}

func (r *RxSticks) ToMSP(channelMap []uint8) rxPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	channels := make([]uint16, 4)
	channels[channelMap[0]] = r.Roll
	channels[channelMap[1]] = r.Pitch
	channels[channelMap[2]] = r.Yaw
	channels[channelMap[3]] = r.Throttle
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

type rxPayload struct {
	Channels []uint16
}
