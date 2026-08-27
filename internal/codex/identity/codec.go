package codexidentity

import (
	"encoding/binary"
	"math"
)

const lengthPrefixBytes = 4

func encodeFields(codec string, fields ...[]byte) ([]byte, error) {
	all := make([][]byte, 0, len(fields)+1)
	all = append(all, []byte(codec))
	all = append(all, fields...)
	total := 0
	for _, field := range all {
		if uint64(len(field)) > math.MaxUint32 || total > math.MaxInt-lengthPrefixBytes-len(field) {
			return nil, errorOf(ErrorInvalidInput, "encoding", "field set is too large", nil)
		}
		total += lengthPrefixBytes + len(field)
	}
	encoded := make([]byte, 0, total)
	var size [lengthPrefixBytes]byte
	for _, field := range all {
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		encoded = append(encoded, size[:]...)
		encoded = append(encoded, field...)
	}
	return encoded, nil
}
