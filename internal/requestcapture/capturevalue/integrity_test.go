package capturevalue

import (
	"encoding/json"
	"testing"
)

func TestCaptureLossesRoundTripAndRejectUnknownReasons(t *testing.T) {
	for losses := CaptureLosses(0); losses < 16; losses++ {
		wire, err := json.Marshal(losses)
		if err != nil {
			t.Fatal(err)
		}
		var result CaptureLosses
		if err := json.Unmarshal(wire, &result); err != nil || result != losses {
			t.Fatalf("round trip %d: %s %d %v", losses, wire, result, err)
		}
	}
	for _, invalid := range []string{"[", `["unknown"]`, "17"} {
		value := CaptureLossRecorderFault
		if err := json.Unmarshal([]byte(invalid), &value); err == nil || value != CaptureLossRecorderFault {
			t.Fatal("invalid reasons silently changed capture integrity")
		}
	}
}
