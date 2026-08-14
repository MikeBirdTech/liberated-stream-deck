package firmwarecapture

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type fixture struct {
	HeaderHex     string `json:"header_hex"`
	PayloadUTF8   string `json:"payload_utf8"`
	PayloadSHA256 string `json:"payload_sha256"`
}

func TestParseSyntheticFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/synthetic-final-report.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var f fixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	header, err := hex.DecodeString(f.HeaderHex)
	if err != nil {
		t.Fatalf("decode fixture header: %v", err)
	}
	if len(header) != HeaderSize {
		t.Fatalf("fixture header size = %d, want %d", len(header), HeaderSize)
	}
	report := make([]byte, ReportSize)
	copy(report, header)
	copy(report[HeaderSize:], f.PayloadUTF8)

	got, err := Parse(bytes.NewReader(report))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if string(got.Payload) != f.PayloadUTF8 {
		t.Fatalf("payload = %q, want %q", got.Payload, f.PayloadUTF8)
	}
	if hex.EncodeToString(got.SHA256[:]) != f.PayloadSHA256 {
		t.Fatalf("SHA-256 = %x, want %s", got.SHA256, f.PayloadSHA256)
	}
	if got.ReportCount != 1 || got.OuterBlockCount != 1 {
		t.Fatalf("counts = %d reports, %d blocks; want 1, 1", got.ReportCount, got.OuterBlockCount)
	}
}

func TestParseReassemblesOuterAndInnerChunks(t *testing.T) {
	payload := make([]byte, OuterBlockSize+37)
	for i := range payload {
		payload[i] = byte((i*29 + 7) % 251)
	}
	raw := syntheticReports(payload)

	got, err := Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatal("reassembled payload differs from input")
	}
	if got.ReportCount != 6 {
		t.Fatalf("report count = %d, want 6", got.ReportCount)
	}
	if got.OuterBlockCount != 2 {
		t.Fatalf("outer block count = %d, want 2", got.OuterBlockCount)
	}
	if want := sha256.Sum256(payload); got.SHA256 != want {
		t.Fatalf("SHA-256 = %x, want %x", got.SHA256, want)
	}
}

func TestParseRejectsMalformedCaptures(t *testing.T) {
	valid := syntheticReports([]byte("fixture-plus-fw\n"))
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "empty capture", mutate: func([]byte) []byte { return nil }},
		{name: "truncated report", mutate: func(in []byte) []byte { return in[:len(in)-1] }},
		{name: "wrong report ID", mutate: setByte(0, 0x03)},
		{name: "wrong command", mutate: setByte(1, 0x04)},
		{name: "wrong outer index", mutate: setByte(2, 0x01)},
		{name: "bad inner done", mutate: setByte(3, 0x02)},
		{name: "bad transfer done", mutate: setByte(4, 0x02)},
		{name: "transfer done without inner done", mutate: setByte(3, 0x00)},
		{name: "wrong inner index", mutate: setByte(5, 0x01)},
		{name: "empty payload", mutate: setUint16(7, 0)},
		{name: "oversize payload", mutate: setUint16(7, PayloadSize+1)},
		{name: "wrong target", mutate: setByte(9, 0x03)},
		{name: "reserved header", mutate: setByte(10, 0x01)},
		{name: "nonzero padding", mutate: setByte(HeaderSize+16, 0x01)},
		{name: "missing transfer done", mutate: setByte(4, 0x00)},
		{name: "report after transfer done", mutate: func(in []byte) []byte { return append(in, in...) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := test.mutate(bytes.Clone(valid))
			if _, err := Parse(bytes.NewReader(input)); err == nil {
				t.Fatal("Parse succeeded; want validation error")
			}
		})
	}
}

func TestParseRejectsShortNonFinalChunk(t *testing.T) {
	raw := syntheticReports(make([]byte, PayloadSize+1))
	binary.LittleEndian.PutUint16(raw[7:9], PayloadSize-1)
	if _, err := Parse(bytes.NewReader(raw)); err == nil {
		t.Fatal("Parse succeeded; want short non-final chunk error")
	}
}

func syntheticReports(payload []byte) []byte {
	var raw []byte
	for outerIndex, outerOffset := 0, 0; outerOffset < len(payload); outerIndex++ {
		outerEnd := min(outerOffset+OuterBlockSize, len(payload))
		for innerIndex, offset := 0, outerOffset; offset < outerEnd; innerIndex++ {
			end := min(offset+PayloadSize, outerEnd)
			report := make([]byte, ReportSize)
			report[0] = outputReportID
			report[1] = updateCommand
			report[2] = byte(outerIndex)
			if end == outerEnd {
				report[3] = 1
			}
			if end == len(payload) {
				report[4] = 1
			}
			binary.LittleEndian.PutUint16(report[5:7], uint16(innerIndex))
			binary.LittleEndian.PutUint16(report[7:9], uint16(end-offset))
			report[9] = updateTarget
			copy(report[HeaderSize:], payload[offset:end])
			raw = append(raw, report...)
			offset = end
		}
		outerOffset = outerEnd
	}
	return raw
}

func setByte(offset int, value byte) func([]byte) []byte {
	return func(in []byte) []byte {
		in[offset] = value
		return in
	}
}

func setUint16(offset, value int) func([]byte) []byte {
	return func(in []byte) []byte {
		binary.LittleEndian.PutUint16(in[offset:offset+2], uint16(value))
		return in
	}
}
