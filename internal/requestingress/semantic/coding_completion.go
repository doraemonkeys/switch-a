package semantic

import "io"

// A decoder may finish before consuming its enclosing representation, or swallow
// an enclosing reader's error returned alongside its last useful bytes. Each
// coding therefore owns its terminal observation independently of its consumer.
type codingReader struct {
	source   io.Reader
	terminal error
}

func (r *codingReader) Read(p []byte) (int, error) {
	if r.terminal != nil {
		return 0, r.terminal
	}
	n, err := r.source.Read(p)
	if err != nil {
		r.terminal = err
	}
	return n, err
}

// Completion proceeds from decoded JSON toward wire. Unused output from an outer
// coding is not additional JSON, but that coding's checksum still must validate.
func finishCodings(stages []*codingReader, wire io.Reader) error {
	for i := len(stages) - 1; i >= 0; i-- {
		if _, err := io.Copy(io.Discard, stages[i]); err != nil {
			return err
		}
	}
	// Some coding formats accept trailing wire data. Preserve that interpretation
	// while still waiting for the source's final read outcome before publishing facts.
	_, err := io.Copy(io.Discard, wire)
	return err
}
