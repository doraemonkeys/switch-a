package capturebridge

import "github.com/doraemonkeys/switch-a/internal/requestcapture"

type Mode uint8

const (
	ModeNone Mode = iota
	ModeTransition
	ModePayload
)

func ModeForRecorder(recorder requestcapture.Recorder) Mode {
	if !recorder.Valid() {
		return ModeNone
	}
	if recorder.CapturesPayload() {
		return ModePayload
	}
	return ModeTransition
}

func (m Mode) Participates() bool {
	return m == ModeTransition || m == ModePayload
}

func (m Mode) CapturesPayload() bool {
	return m == ModePayload
}
