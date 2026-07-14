package textio

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unicode/utf16"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

func TestStreamCanonicalDetectsStrictBOMMatrix(t *testing.T) {
	tests := []struct {
		name     string
		raw      []byte
		encoding Encoding
		want     string
	}{
		{name: "empty", encoding: EncodingUTF8},
		{name: "utf8", raw: []byte("alpha\n"), encoding: EncodingUTF8, want: "alpha\n"},
		{name: "utf8_bom", raw: append([]byte{0xef, 0xbb, 0xbf}, []byte("alpha\n")...), encoding: EncodingUTF8, want: "alpha\n"},
		{name: "utf16be", raw: encodeUTF16([]rune("alpha\n"), binary.BigEndian), encoding: EncodingUTF16BE, want: "alpha\n"},
		{name: "utf16le", raw: encodeUTF16([]rune("alpha\n"), binary.LittleEndian), encoding: EncodingUTF16LE, want: "alpha\n"},
		{name: "utf32be", raw: encodeUTF32([]rune("alpha\n"), binary.BigEndian), encoding: EncodingUTF32BE, want: "alpha\n"},
		{name: "utf32le", raw: encodeUTF32([]rune("alpha\n"), binary.LittleEndian), encoding: EncodingUTF32LE, want: "alpha\n"},
		{name: "utf32le_bom_precedes_utf16le", raw: []byte{0xff, 0xfe, 0x00, 0x00}, encoding: EncodingUTF32LE},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := openTextFile(t, test.raw)
			defer file.Close()

			sink := &collectSink{}
			summary, code := StreamCanonical(context.Background(), file, Domain{}, unlimitedBudget(), sink)
			if code != "" {
				t.Fatalf("StreamCanonical() code = %q", code)
			}
			if summary.Encoding != test.encoding {
				t.Fatalf("Encoding = %q, want %q", summary.Encoding, test.encoding)
			}
			if got := sink.canonical(summary.FinalLF); got != test.want {
				t.Fatalf("canonical = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStreamCanonicalRejectsInvalidScalarsAndNUL(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want api.ErrorCode
	}{
		{name: "invalid_utf8", raw: []byte{0xc0, 0x80}, want: api.ErrorUnsupportedEncoding},
		{name: "utf8_nul", raw: []byte{'a', 0}, want: api.ErrorBinary},
		{name: "utf16_odd", raw: []byte{0xff, 0xfe, 'a'}, want: api.ErrorUnsupportedEncoding},
		{name: "utf16_unpaired_high", raw: []byte{0xff, 0xfe, 0x00, 0xd8}, want: api.ErrorUnsupportedEncoding},
		{name: "utf16_unpaired_low", raw: []byte{0xff, 0xfe, 0x00, 0xdc}, want: api.ErrorUnsupportedEncoding},
		{name: "utf16_nul", raw: []byte{0xff, 0xfe, 0x61, 0x00, 0x00, 0x00}, want: api.ErrorBinary},
		{name: "utf32_incomplete", raw: []byte{0, 0, 0xfe, 0xff, 0, 0}, want: api.ErrorUnsupportedEncoding},
		{name: "utf32_surrogate", raw: []byte{0, 0, 0xfe, 0xff, 0, 0, 0xd8, 0}, want: api.ErrorUnsupportedEncoding},
		{name: "utf32_non_scalar", raw: []byte{0, 0, 0xfe, 0xff, 0, 0x11, 0, 0}, want: api.ErrorUnsupportedEncoding},
		{name: "utf32_nul", raw: []byte{0, 0, 0xfe, 0xff, 0, 0, 0, 0}, want: api.ErrorBinary},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := openTextFile(t, test.raw)
			defer file.Close()
			_, code := StreamCanonical(context.Background(), file, Domain{}, unlimitedBudget(), &collectSink{})
			if code != test.want {
				t.Fatalf("code = %q, want %q", code, test.want)
			}
		})
	}
}

type collectSink struct {
	lines [][]byte
}

func (sink *collectSink) Consume(line Line) error {
	sink.lines = append(sink.lines, append([]byte(nil), line.Bytes...))
	return nil
}

func (sink *collectSink) canonical(finalLF bool) string {
	var result []byte
	for index, line := range sink.lines {
		result = append(result, line...)
		if index+1 < len(sink.lines) || finalLF {
			result = append(result, '\n')
		}
	}
	return string(result)
}

func openTextFile(t *testing.T, content []byte) *rootfs.File {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "input.txt"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	target := pathspec.POSIX
	if runtime.GOOS == "windows" {
		target = pathspec.Windows
	}
	rootPath, code := pathspec.ParseRootDirectory(target, directory)
	if code != "" {
		t.Fatalf("ParseRootDirectory() code = %q", code)
	}
	root, err := rootfs.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	lease, err := root.Duplicate()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	relative, code := pathspec.ParseRelative(target, "input.txt", false)
	if code != "" {
		t.Fatalf("ParseRelative() code = %q", code)
	}
	file, err := lease.OpenRegular(relative)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func unlimitedBudget() Budget {
	return Budget{MaxRawBytes: ^uint64(0)}
}

func encodeUTF16(value []rune, order binary.ByteOrder) []byte {
	units := utf16.Encode(value)
	result := make([]byte, 2, 2+len(units)*2)
	if order == binary.BigEndian {
		copy(result, []byte{0xfe, 0xff})
	} else {
		copy(result, []byte{0xff, 0xfe})
	}
	for _, unit := range units {
		var encoded [2]byte
		order.PutUint16(encoded[:], unit)
		result = append(result, encoded[:]...)
	}
	return result
}

func encodeUTF32(value []rune, order binary.ByteOrder) []byte {
	result := make([]byte, 4, 4+len(value)*4)
	if order == binary.BigEndian {
		copy(result, []byte{0x00, 0x00, 0xfe, 0xff})
	} else {
		copy(result, []byte{0xff, 0xfe, 0x00, 0x00})
	}
	for _, character := range value {
		var encoded [4]byte
		order.PutUint32(encoded[:], uint32(character))
		result = append(result, encoded[:]...)
	}
	return result
}
