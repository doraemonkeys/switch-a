package tokenanalytics

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestClientAPIKeysSnapshotLifecycle(t *testing.T) {
	cause := errors.New("storage unavailable")
	expected := []ClientAPIKey{{Fingerprint: "key-fingerprint", Name: "Laptop"}}
	for _, test := range []struct {
		name   string
		reader *fakeReader
		want   []ClientAPIKey
		stage  FailureStage
	}{
		{"keys", &fakeReader{snapshot: &fakeSnapshot{keys: expected}}, expected, ""},
		{"empty", &fakeReader{snapshot: &fakeSnapshot{}}, []ClientAPIKey{}, ""},
		{"open failure", &fakeReader{err: cause}, nil, FailureStageSnapshotOpen},
		{"nil snapshot", &fakeReader{}, nil, FailureStageSnapshotOpen},
		{"read failure", &fakeReader{snapshot: &fakeSnapshot{keysErr: cause}}, nil, FailureStageClientAPIKeys},
		{"close failure", &fakeReader{snapshot: &fakeSnapshot{closeErr: cause}}, nil, FailureStageClientAPIKeys},
		{"read and close failure", &fakeReader{snapshot: &fakeSnapshot{keysErr: cause, closeErr: cause}}, nil, FailureStageClientAPIKeys},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NewService(test.reader).ClientAPIKeys(context.Background())
			if !reflect.DeepEqual(got, test.want) || (test.stage == "" && err != nil) || (test.stage != "" && !IsFailureAt(err, test.stage)) {
				t.Fatalf("ClientAPIKeys() = %+v, %v", got, err)
			}
			if snapshot, ok := test.reader.snapshot.(*fakeSnapshot); ok && snapshot.closed != 1 {
				t.Fatalf("closed = %d", snapshot.closed)
			}
		})
	}
}
