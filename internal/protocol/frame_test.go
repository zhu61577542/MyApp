package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"testing"
)

type slowWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *slowWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit {
		data = data[:w.limit]
	}
	return w.buffer.Write(data)
}

type slowReader struct {
	reader io.Reader
	limit  int
}

func (r slowReader) Read(data []byte) (int, error) {
	if len(data) > r.limit {
		data = data[:r.limit]
	}
	return r.reader.Read(data)
}

func TestFrameRoundTripWithFragmentedIO(t *testing.T) {
	writer := &slowWriter{limit: 3}
	want := Frame{Type: TypeClipboard, Flags: FlagCritical, StreamID: 42, Payload: []byte("中文 clipboard")}
	if err := want.Encode(writer, 1024); err != nil {
		t.Fatal(err)
	}
	got, err := (Decoder{MaxPayload: 1024}).Decode(slowReader{reader: &writer.buffer, limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	want.Major, want.Minor = CurrentMajor, CurrentMinor
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("帧不一致: got=%+v want=%+v", got, want)
	}
}

func TestDecoderRejectsInvalidHeadersBeforeAllocation(t *testing.T) {
	valid := make([]byte, HeaderSize)
	copy(valid, magic[:])
	valid[4] = CurrentMajor
	valid[6] = byte(TypeHello)

	for name, testCase := range map[string]struct {
		mutate func([]byte)
		target error
	}{
		"magic":            {func(b []byte) { b[0] = 0 }, ErrBadMagic},
		"version":          {func(b []byte) { b[4] = 9 }, ErrVersion},
		"flags":            {func(b []byte) { b[7] = 0x80 }, ErrFlags},
		"reserved":         {func(b []byte) { b[23] = 1 }, ErrReserved},
		"large":            {func(b []byte) { binary.BigEndian.PutUint32(b[16:20], 2048) }, ErrPayloadTooLarge},
		"unknown critical": {func(b []byte) { b[6], b[7] = 99, byte(FlagCritical) }, ErrUnknownMessage},
	} {
		t.Run(name, func(t *testing.T) {
			header := append([]byte(nil), valid...)
			testCase.mutate(header)
			_, err := (Decoder{MaxPayload: 1024}).Decode(bytes.NewReader(header))
			if !errors.Is(err, testCase.target) {
				t.Fatalf("错误=%v，期望=%v", err, testCase.target)
			}
		})
	}
}

func TestPayloadLimitOnEncode(t *testing.T) {
	err := (Frame{Type: TypeHello, Payload: make([]byte, 5)}).Encode(io.Discard, 4)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("错误=%v", err)
	}
}
