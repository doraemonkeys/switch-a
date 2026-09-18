package capturevalue

import (
	"encoding/json"
	"fmt"
)

// CaptureLosses records independent reasons that an observer could not preserve
// evidence. A fixed set survives quota exhaustion without allocating more storage.
type CaptureLosses uint8

const (
	CaptureLossMemoryBudget CaptureLosses = 1 << iota
	CaptureLossMetadataUnavailable
	CaptureLossRecorderFault
	CaptureLossIngressGap
)

var captureLossNames = [...]string{
	"memory_budget",
	"metadata_unavailable",
	"recorder_fault",
	"ingress_gap",
}

func (losses CaptureLosses) Names() []string {
	result := make([]string, 0, len(captureLossNames))
	for index, name := range captureLossNames {
		if losses&(1<<index) != 0 {
			result = append(result, name)
		}
	}
	return result
}

func (losses CaptureLosses) MarshalJSON() ([]byte, error) {
	return json.Marshal(losses.Names())
}

func (losses *CaptureLosses) UnmarshalJSON(data []byte) error {
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return err
	}
	var result CaptureLosses
	for _, name := range names {
		found := false
		for index, known := range captureLossNames {
			if name == known {
				result |= 1 << index
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown capture loss %q", name)
		}
	}
	*losses = result
	return nil
}
