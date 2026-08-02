package exportwire

import (
	"crypto/sha256"
	"hash"
)

type MetadataStream struct {
	writer      *Writer
	manifest    bool
	recordIndex int
	buffer      []byte
	length      int
	hasher      hash.Hash
	rawSize     int64
	chunkCount  int
}

func NewMetadataStream(
	writer *Writer,
	manifest bool,
	recordIndex int,
) *MetadataStream {
	buffer := writer.metadataBuffer
	fragmentBytes := MetadataRawFragmentBytes(len(writer.line.storage))
	if len(buffer) > fragmentBytes {
		buffer = buffer[:fragmentBytes]
	}
	return &MetadataStream{
		writer:      writer,
		manifest:    manifest,
		recordIndex: recordIndex,
		buffer:      buffer,
		hasher:      sha256.New(),
	}
}

func (stream *MetadataStream) WriteByte(value byte) error {
	if stream.length == len(stream.buffer) {
		if err := stream.flush(); err != nil {
			return err
		}
	}
	stream.buffer[stream.length] = value
	stream.length++
	return nil
}

func (stream *MetadataStream) WriteString(value string) error {
	for len(value) > 0 {
		if stream.length == len(stream.buffer) {
			if err := stream.flush(); err != nil {
				return err
			}
		}
		written := copy(stream.buffer[stream.length:], value)
		stream.length += written
		value = value[written:]
	}
	return nil
}

func (stream *MetadataStream) WriteBytes(value []byte) error {
	for len(value) > 0 {
		if stream.length == len(stream.buffer) {
			if err := stream.flush(); err != nil {
				return err
			}
		}
		written := copy(stream.buffer[stream.length:], value)
		stream.length += written
		value = value[written:]
	}
	return nil
}

func (stream *MetadataStream) flush() error {
	if stream.length == 0 {
		return nil
	}
	raw := stream.buffer[:stream.length]
	if _, err := stream.hasher.Write(raw); err != nil {
		return err
	}
	checksum := CurrentSHA256(stream.hasher)
	rawOffset := stream.rawSize
	stream.rawSize += int64(len(raw))
	if err := stream.writer.WriteMetadataChunk(
		stream.manifest,
		stream.recordIndex,
		stream.chunkCount,
		rawOffset,
		raw,
		checksum,
	); err != nil {
		return err
	}
	stream.chunkCount++
	stream.length = 0
	return nil
}

func (stream *MetadataStream) Finish() (Digest, error) {
	if err := stream.flush(); err != nil {
		return Digest{}, err
	}
	return Digest{
		RawSize:    stream.rawSize,
		ChunkCount: stream.chunkCount,
		Checksum:   CurrentSHA256(stream.hasher),
	}, nil
}

func CurrentSHA256(hasher hash.Hash) [sha256.Size]byte {
	var storage [sha256.Size]byte
	sum := hasher.Sum(storage[:0])
	var result [sha256.Size]byte
	copy(result[:], sum)
	return result
}
