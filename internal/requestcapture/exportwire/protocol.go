package exportwire

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strconv"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/jsonstream"
)

const (
	FormatVersion                   = 2
	hasherReserveBytes        int64 = 512
	maxDecimalBytes                 = 20
	base64QuantumRawBytes           = 3
	base64QuantumEncodedBytes       = 4

	// Every arbitrary-length value travels in base64 fragments. Reserving the
	// maximum width of each numeric field makes the line bound structural on
	// both 32-bit and 64-bit platforms.
	blobDataEnvelopeMaxBytes = len(`{"version":`) + maxDecimalBytes +
		len(`,"event":"blob_chunk","part":"data","record_index":`) + maxDecimalBytes +
		len(`,"blob_index":`) + maxDecimalBytes +
		len(`,"chunk_index":`) + maxDecimalBytes +
		len(`,"raw_offset":`) + maxDecimalBytes +
		len(`,"raw_size":`) + maxDecimalBytes +
		len(`,"raw_total_size":`) + maxDecimalBytes +
		len(`,"data_base64":"`) +
		len(`","cumulative_sha256":"`) + sha256.Size*2 +
		len("\"}\n")
	metadataEnvelopeMaxBytes = len(`{"version":`) + maxDecimalBytes +
		len(`,"event":"record","part":"metadata_chunk","record_index":`) + maxDecimalBytes +
		len(`,"chunk_index":`) + maxDecimalBytes +
		len(`,"raw_offset":`) + maxDecimalBytes +
		len(`,"raw_size":`) + maxDecimalBytes +
		len(`,"data_base64":"`) +
		len(`","cumulative_sha256":"`) + sha256.Size*2 +
		len("\"}\n")
)

var ErrLineTooLarge = errors.New("request capture export line exceeds configured limit")

func rawFragmentBytes(lineBytes, envelopeBytes int) int {
	encodedBytes := lineBytes - envelopeBytes
	if encodedBytes < base64QuantumEncodedBytes {
		return 0
	}
	return (encodedBytes / base64QuantumEncodedBytes) * base64QuantumRawBytes
}

func BlobRawFragmentBytes(lineBytes int) int {
	return rawFragmentBytes(lineBytes, blobDataEnvelopeMaxBytes)
}

func MetadataRawFragmentBytes(lineBytes int) int {
	return rawFragmentBytes(lineBytes, metadataEnvelopeMaxBytes)
}

func MinimumLineBytes() int {
	envelopeBytes := max(blobDataEnvelopeMaxBytes, metadataEnvelopeMaxBytes)
	return envelopeBytes + base64QuantumEncodedBytes
}

func WorkspaceSizing(lineBytes int) (int, int64, bool) {
	metadataBytes := MetadataRawFragmentBytes(lineBytes)
	maxInt := int(^uint(0) >> 1)
	if lineBytes <= 0 || metadataBytes <= 0 || lineBytes > maxInt-metadataBytes {
		return 0, 0, false
	}
	workspaceBytes := lineBytes + metadataBytes
	if int64(workspaceBytes) > math.MaxInt64-hasherReserveBytes {
		return 0, 0, false
	}
	return workspaceBytes, int64(workspaceBytes) + hasherReserveBytes, true
}

type Digest struct {
	RawSize    int64
	ChunkCount int
	Checksum   [sha256.Size]byte
}

type fixedJSONLine struct {
	storage []byte
	length  int
}

func (line *fixedJSONLine) reset() {
	line.length = 0
}

func (line *fixedJSONLine) WriteByte(value byte) error {
	if line.length == len(line.storage) {
		return ErrLineTooLarge
	}
	line.storage[line.length] = value
	line.length++
	return nil
}

func (line *fixedJSONLine) WriteString(value string) error {
	if len(value) > len(line.storage)-line.length {
		return ErrLineTooLarge
	}
	copy(line.storage[line.length:], value)
	line.length += len(value)
	return nil
}

func (line *fixedJSONLine) WriteBytes(value []byte) error {
	if len(value) > len(line.storage)-line.length {
		return ErrLineTooLarge
	}
	copy(line.storage[line.length:], value)
	line.length += len(value)
	return nil
}

func (line *fixedJSONLine) writeInt64(value int64) error {
	var encoded [32]byte
	return line.WriteBytes(strconv.AppendInt(encoded[:0], value, 10))
}

func (line *fixedJSONLine) writeUint64(value uint64) error {
	var encoded [32]byte
	return line.WriteBytes(strconv.AppendUint(encoded[:0], value, 10))
}

func (line *fixedJSONLine) writeInt(value int) error {
	return line.writeInt64(int64(value))
}

func (line *fixedJSONLine) writeBase64(value []byte) error {
	encodedSize := base64.StdEncoding.EncodedLen(len(value))
	if encodedSize > len(line.storage)-line.length {
		return ErrLineTooLarge
	}
	base64.StdEncoding.Encode(line.storage[line.length:line.length+encodedSize], value)
	line.length += encodedSize
	return nil
}

func (line *fixedJSONLine) writeChecksum(value [sha256.Size]byte) error {
	encodedSize := hex.EncodedLen(len(value))
	if encodedSize > len(line.storage)-line.length {
		return ErrLineTooLarge
	}
	hex.Encode(line.storage[line.length:line.length+encodedSize], value[:])
	line.length += encodedSize
	return nil
}

func (line *fixedJSONLine) bytes() []byte {
	return line.storage[:line.length]
}

type Writer struct {
	destination    io.Writer
	line           fixedJSONLine
	metadataBuffer []byte
	riskNotice     string
	check          func() error
}

// NewWriter binds caller-owned fixed workspace to one streaming operation.
func NewWriter(
	destination io.Writer,
	lineStorage []byte,
	metadataBuffer []byte,
	riskNotice string,
	check func() error,
) Writer {
	return Writer{
		destination:    destination,
		line:           fixedJSONLine{storage: lineStorage},
		metadataBuffer: metadataBuffer,
		riskNotice:     riskNotice,
		check:          check,
	}
}

func (writer *Writer) LineBytes() int {
	return len(writer.line.storage)
}

func (writer *Writer) beginLine() error {
	if err := writer.check(); err != nil {
		return err
	}
	writer.line.reset()
	return nil
}

func (writer *Writer) writeEnvelopePrefix(suffix string) error {
	if err := writer.line.WriteString(`{"version":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(FormatVersion); err != nil {
		return err
	}
	return writer.line.WriteString(suffix)
}

func (writer *Writer) commitLine() error {
	if err := writer.line.WriteByte('\n'); err != nil {
		return err
	}
	payload := writer.line.bytes()
	written, err := writer.destination.Write(payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}

func (writer *Writer) WriteManifestBegin(recordCount int) error {
	if err := writer.beginLine(); err != nil {
		return err
	}
	if err := writer.writeEnvelopePrefix(`,"event":"manifest","part":"begin","record_count":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(recordCount); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"metadata_encoding":"json","raw_payload_risk":`); err != nil {
		return err
	}
	if err := jsonstream.WriteString(&writer.line, writer.riskNotice); err != nil {
		return err
	}
	if err := writer.line.WriteByte('}'); err != nil {
		return err
	}
	return writer.commitLine()
}

func (writer *Writer) WriteRecordBegin(recordIndex, blobCount int) error {
	if err := writer.beginLine(); err != nil {
		return err
	}
	if err := writer.writeEnvelopePrefix(`,"event":"record","part":"begin","record_index":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(recordIndex); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"blob_count":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(blobCount); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"metadata_encoding":"json"}`); err != nil {
		return err
	}
	return writer.commitLine()
}

func (writer *Writer) writeMetadataChunkPrefix(manifest bool, recordIndex int) error {
	if manifest {
		return writer.writeEnvelopePrefix(`,"event":"manifest","part":"metadata_chunk","chunk_index":`)
	}
	if err := writer.writeEnvelopePrefix(`,"event":"record","part":"metadata_chunk","record_index":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(recordIndex); err != nil {
		return err
	}
	return writer.line.WriteString(`,"chunk_index":`)
}

func (writer *Writer) writeMetadataEndPrefix(manifest bool, recordIndex int) error {
	if manifest {
		return writer.writeEnvelopePrefix(`,"event":"manifest","part":"metadata_end","chunk_count":`)
	}
	if err := writer.writeEnvelopePrefix(`,"event":"record","part":"metadata_end","record_index":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(recordIndex); err != nil {
		return err
	}
	return writer.line.WriteString(`,"chunk_count":`)
}

func (writer *Writer) WriteMetadataChunk(
	manifest bool,
	recordIndex int,
	chunkIndex int,
	rawOffset int64,
	raw []byte,
	cumulative [sha256.Size]byte,
) error {
	if err := writer.beginLine(); err != nil {
		return err
	}
	if err := writer.writeMetadataChunkPrefix(manifest, recordIndex); err != nil {
		return err
	}
	if err := writer.line.writeInt(chunkIndex); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"raw_offset":`); err != nil {
		return err
	}
	if err := writer.line.writeInt64(rawOffset); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"raw_size":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(len(raw)); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"data_base64":"`); err != nil {
		return err
	}
	if err := writer.line.writeBase64(raw); err != nil {
		return err
	}
	if err := writer.line.WriteString(`","cumulative_sha256":"`); err != nil {
		return err
	}
	if err := writer.line.writeChecksum(cumulative); err != nil {
		return err
	}
	if err := writer.line.WriteString(`"}`); err != nil {
		return err
	}
	return writer.commitLine()
}

func (writer *Writer) WriteMetadataEnd(
	manifest bool,
	recordIndex int,
	summary Digest,
) error {
	if err := writer.beginLine(); err != nil {
		return err
	}
	if err := writer.writeMetadataEndPrefix(manifest, recordIndex); err != nil {
		return err
	}
	if err := writer.line.writeInt(summary.ChunkCount); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"raw_size":`); err != nil {
		return err
	}
	if err := writer.line.writeInt64(summary.RawSize); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"final_sha256":"`); err != nil {
		return err
	}
	if err := writer.line.writeChecksum(summary.Checksum); err != nil {
		return err
	}
	if err := writer.line.WriteString(`"}`); err != nil {
		return err
	}
	return writer.commitLine()
}

func (writer *Writer) WriteBlobData(
	recordIndex int,
	blobIndex int,
	chunkIndex int,
	rawOffset int64,
	rawTotalSize int64,
	raw []byte,
	cumulative [sha256.Size]byte,
) error {
	if err := writer.beginLine(); err != nil {
		return err
	}
	if err := writer.writeEnvelopePrefix(`,"event":"blob_chunk","part":"data","record_index":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(recordIndex); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"blob_index":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(blobIndex); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"chunk_index":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(chunkIndex); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"raw_offset":`); err != nil {
		return err
	}
	if err := writer.line.writeInt64(rawOffset); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"raw_size":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(len(raw)); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"raw_total_size":`); err != nil {
		return err
	}
	if err := writer.line.writeInt64(rawTotalSize); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"data_base64":"`); err != nil {
		return err
	}
	if err := writer.line.writeBase64(raw); err != nil {
		return err
	}
	if err := writer.line.WriteString(`","cumulative_sha256":"`); err != nil {
		return err
	}
	if err := writer.line.writeChecksum(cumulative); err != nil {
		return err
	}
	if err := writer.line.WriteString(`"}`); err != nil {
		return err
	}
	return writer.commitLine()
}

func (writer *Writer) WriteBlobEnd(
	recordIndex int,
	blobIndex int,
	summary Digest,
) error {
	if err := writer.beginLine(); err != nil {
		return err
	}
	if err := writer.writeEnvelopePrefix(`,"event":"blob_chunk","part":"end","record_index":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(recordIndex); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"blob_index":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(blobIndex); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"chunk_count":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(summary.ChunkCount); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"raw_size":`); err != nil {
		return err
	}
	if err := writer.line.writeInt64(summary.RawSize); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"final_sha256":"`); err != nil {
		return err
	}
	if err := writer.line.writeChecksum(summary.Checksum); err != nil {
		return err
	}
	if err := writer.line.WriteString(`"}`); err != nil {
		return err
	}
	return writer.commitLine()
}

func (writer *Writer) WriteRecordEnd(
	recordIndex int,
	blobCount int,
	rawPayloadSize int64,
	metadata Digest,
) error {
	if err := writer.beginLine(); err != nil {
		return err
	}
	if err := writer.writeEnvelopePrefix(`,"event":"record_end","record_index":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(recordIndex); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"blob_count":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(blobCount); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"raw_payload_size":`); err != nil {
		return err
	}
	if err := writer.line.writeInt64(rawPayloadSize); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"metadata_raw_size":`); err != nil {
		return err
	}
	if err := writer.line.writeInt64(metadata.RawSize); err != nil {
		return err
	}
	if err := writer.line.WriteString(`,"metadata_sha256":"`); err != nil {
		return err
	}
	if err := writer.line.writeChecksum(metadata.Checksum); err != nil {
		return err
	}
	if err := writer.line.WriteString(`"}`); err != nil {
		return err
	}
	return writer.commitLine()
}

func (writer *Writer) WriteExportEnd(recordCount int) error {
	if err := writer.beginLine(); err != nil {
		return err
	}
	if err := writer.writeEnvelopePrefix(`,"event":"export_end","record_count":`); err != nil {
		return err
	}
	if err := writer.line.writeInt(recordCount); err != nil {
		return err
	}
	if err := writer.line.WriteByte('}'); err != nil {
		return err
	}
	return writer.commitLine()
}
