package jsonwire

import (
	"errors"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

func FuzzScanObject(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"jsonrpc":"2.0","id":1e3,"method":"tools/call","params":{"name":"read","arguments":{"path":"a"},"_meta":{"progressToken":"p"}}}`),
		[]byte(`{"method":"tools/call","params":{"arguments":{"x":1,"x":2}}}`),
		[]byte(`{"x":1,"\u0078":2}`),
		[]byte(`{"x":"\uD800"}`),
		[]byte(`{"x":[1,}`),
		[]byte(`01`),
		[]byte(`1e+`),
		[]byte{'"', 0xff, '"'},
		nestedArrays(9),
		objectWithMembers(17),
		arrayWithItems(65),
		[]byte(`{"` + strings.Repeat("k", 65) + `":0}`),
		[]byte(`"` + strings.Repeat("x", 257) + `"`),
		[]byte(strings.Repeat("9", 33)),
	} {
		f.Add(seed)
	}

	limits := Limits{
		MaxDepth:          8,
		MaxObjectMembers:  16,
		MaxContainerItems: 64,
		MaxKeyBytes:       64,
		MaxStringBytes:    256,
		MaxNumberRawBytes: 32,
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 65_536 {
			t.Skip()
		}
		for _, mode := range []Mode{ValidateAll, ProtocolWithRawArguments, ToolArguments} {
			kind, span, err := scanDocument(raw, limits, mode)
			kindAgain, spanAgain, errAgain := scanDocument(raw, limits, mode)
			if kind != kindAgain || span != spanAgain || validationErrorSignature(t, err) != validationErrorSignature(t, errAgain) {
				t.Fatalf("non-deterministic scan in mode %d: (%d, %#v, %v) / (%d, %#v, %v)", mode, kind, span, err, kindAgain, spanAgain, errAgain)
			}
			if err != nil {
				assertFixedValidationError(t, err)
				_, viewErr := ScanObject(raw, limits, mode)
				if validationErrorSignature(t, viewErr) != validationErrorSignature(t, err) {
					t.Fatalf("ScanObject error = %v, structural error = %v", viewErr, err)
				}
				continue
			}
			if span.Start < 0 || span.End > len(raw) || span.Start >= span.End {
				t.Fatalf("accepted span %#v outside %d bytes", span, len(raw))
			}
			rescannedKind, rescannedSpan, rescannedErr := scanDocument(raw[span.Start:span.End], limits, mode)
			if rescannedErr != nil || rescannedKind != kind || rescannedSpan != (Span{Start: 0, End: span.End - span.Start}) {
				t.Fatalf("accepted value was not stable on re-scan: (%d, %#v, %v)", rescannedKind, rescannedSpan, rescannedErr)
			}

			view, viewErr := ScanObject(raw, limits, mode)
			if kind != Object {
				var validationError *ValidationError
				if !errors.As(viewErr, &validationError) || validationError.Kind() != KindMismatch {
					t.Fatalf("ScanObject non-object error = %v, want kind mismatch", viewErr)
				}
				continue
			}
			if viewErr != nil {
				t.Fatalf("ScanObject accepted structure error = %v", viewErr)
			}
			if uint64(len(view.members)) > limits.MaxObjectMembers {
				t.Fatalf("retained %d members above cap %d", len(view.members), limits.MaxObjectMembers)
			}
			for _, member := range view.members {
				if member.KeySpan.Start < 0 || member.KeySpan.End > len(view.raw) || member.KeySpan.Start >= member.KeySpan.End ||
					member.Value.Start < 0 || member.Value.End > len(view.raw) || member.Value.Start >= member.Value.End {
					t.Fatalf("member spans outside owned input: %#v", member)
				}
				if uint64(len(member.Key)) > limits.MaxKeyBytes {
					t.Fatalf("retained key has %d bytes above cap %d", len(member.Key), limits.MaxKeyBytes)
				}
			}
		}

		protocol, protocolErr := scanProtocolDocumentDetailed(raw, limits)
		protocolAgain, protocolErrAgain := scanProtocolDocumentDetailed(raw, limits)
		if !reflect.DeepEqual(protocol, protocolAgain) || validationErrorSignature(t, protocolErr) != validationErrorSignature(t, protocolErrAgain) {
			t.Fatalf("non-deterministic protocol scan: (%#v, %v) / (%#v, %v)", protocol, protocolErr, protocolAgain, protocolErrAgain)
		}
		var protocolValidationError *ValidationError
		if errors.As(protocolErr, &protocolValidationError) && protocolValidationError.Scope() == ScopeDocument && !reflect.DeepEqual(protocol, documentScanResult{}) {
			t.Fatalf("document-scoped failure exposed partial protocol state: %#v", protocol)
		}
	})
}

func TestScannerAllocationsStayBoundedAtEveryProtocolCap(t *testing.T) {
	limits := protocolTestLimits()
	for _, test := range []struct {
		name      string
		raw       []byte
		maxAllocs float64
	}{
		{name: "depth", raw: nestedArrays(64), maxAllocs: 32},
		{name: "object members", raw: objectWithMembers(4_096), maxAllocs: 10_000},
		{name: "container items", raw: arrayWithItems(65_536), maxAllocs: 32},
		{name: "key bytes", raw: []byte(`{"` + strings.Repeat("k", 4_096) + `":0}`), maxAllocs: 32},
		{name: "string bytes", raw: []byte(`"` + strings.Repeat("x", 262_144) + `"`), maxAllocs: 8},
		{name: "number bytes", raw: []byte(strings.Repeat("9", 256)), maxAllocs: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireScanOK(t, test.raw, limits)
			allocations := testing.AllocsPerRun(3, func() {
				if _, _, err := scanDocument(test.raw, limits, ValidateAll); err != nil {
					panic(err)
				}
			})
			if allocations > test.maxAllocs {
				t.Fatalf("allocations = %.0f, want <= %.0f", allocations, test.maxAllocs)
			}
		})
	}
}

func TestScannerStreamsArrayItemsWithoutRetainingFrameCount(t *testing.T) {
	limits := protocolTestLimits()
	small := arrayWithItems(1)
	large := arrayWithItems(65_536)
	smallAllocs := testing.AllocsPerRun(5, func() {
		_, _, _ = scanDocument(small, limits, ValidateAll)
	})
	largeAllocs := testing.AllocsPerRun(5, func() {
		_, _, _ = scanDocument(large, limits, ValidateAll)
	})
	if largeAllocs > smallAllocs+2 {
		t.Fatalf("array allocations grew with item count: small %.0f, large %.0f", smallAllocs, largeAllocs)
	}
}

func TestScannerAllocatedBytesStayBoundedAtEveryProtocolCap(t *testing.T) {
	limits := protocolTestLimits()
	for _, test := range []struct {
		name            string
		raw             []byte
		maxBytesPerScan uint64
	}{
		{name: "depth", raw: nestedArrays(64), maxBytesPerScan: 128 << 10},
		{name: "object members", raw: objectWithMembers(4_096), maxBytesPerScan: 2 << 20},
		{name: "container items", raw: arrayWithItems(65_536), maxBytesPerScan: 128 << 10},
		{name: "key bytes", raw: []byte(`{"` + strings.Repeat("k", 4_096) + `":0}`), maxBytesPerScan: 128 << 10},
		{name: "string bytes", raw: []byte(`"` + strings.Repeat("x", 262_144) + `"`), maxBytesPerScan: 128 << 10},
		{name: "number bytes", raw: []byte(strings.Repeat("9", 256)), maxBytesPerScan: 32 << 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireScanOK(t, test.raw, limits)
			bytesPerScan := measureAllocatedBytesPerRun(3, func() {
				if _, _, err := scanDocument(test.raw, limits, ValidateAll); err != nil {
					panic(err)
				}
			})
			t.Logf("input=%d allocated_bytes_per_scan=%d ceiling=%d", len(test.raw), bytesPerScan, test.maxBytesPerScan)
			if bytesPerScan > test.maxBytesPerScan {
				t.Fatalf("allocated bytes per scan = %d, want <= %d", bytesPerScan, test.maxBytesPerScan)
			}
		})
	}
}

func TestScannerAllocatedBytesDoNotScaleWithFrameOrItemVolume(t *testing.T) {
	limits := protocolTestLimits()
	for _, test := range []struct {
		name  string
		small []byte
		large []byte
	}{
		{
			name:  "frame bytes",
			small: []byte(`"x"`),
			large: []byte(`"` + strings.Repeat("x", 262_144) + `"`),
		},
		{
			name:  "array items",
			small: arrayWithItems(1),
			large: arrayWithItems(65_536),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireScanOK(t, test.small, limits)
			requireScanOK(t, test.large, limits)
			smallBytes := measureAllocatedBytesPerRun(3, func() {
				_, _, _ = scanDocument(test.small, limits, ValidateAll)
			})
			largeBytes := measureAllocatedBytesPerRun(3, func() {
				_, _, _ = scanDocument(test.large, limits, ValidateAll)
			})
			t.Logf("small_input=%d small_allocated=%d large_input=%d large_allocated=%d", len(test.small), smallBytes, len(test.large), largeBytes)
			if largeBytes > smallBytes+(4<<10) {
				t.Fatalf("allocated bytes grew with input volume: small %d, large %d", smallBytes, largeBytes)
			}
		})
	}
}

func TestScannerAllocatedBytesStayBoundedForNestedDuplicateErrors(t *testing.T) {
	limits := protocolTestLimits()
	for _, depth := range []int{0, 63} {
		raw := nestedDuplicateObject(depth)
		requireScanErrorKind(t, raw, limits, KindDuplicate)
		bytesPerScan := measureAllocatedBytesPerRun(3, func() {
			_, _, _ = scanDocument(raw, limits, ValidateAll)
		})
		t.Logf("nested_depth=%d input=%d allocated_bytes_per_scan=%d", depth+1, len(raw), bytesPerScan)
		if bytesPerScan > 512<<10 {
			t.Fatalf("depth %d allocated bytes per scan = %d, want <= %d", depth+1, bytesPerScan, 512<<10)
		}
	}
}

func TestProtocolRecoveryAllocatedBytesStayBoundedByInputAndLimits(t *testing.T) {
	baseLimits := protocolTestLimits()
	for _, test := range []struct {
		name      string
		params    []byte
		configure func(*Limits)
	}{
		{
			name:   "depth overflow",
			params: nestedArrays(32_768),
			configure: func(limits *Limits) {
				limits.MaxDepth = 8
			},
		},
		{
			name:   "item overflow",
			params: arrayWithItems(65_536),
			configure: func(limits *Limits) {
				limits.MaxContainerItems = 32
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			limits := baseLimits
			test.configure(&limits)
			raw := make([]byte, 0, len(test.params)+80)
			raw = append(raw, `{"params":`...)
			raw = append(raw, test.params...)
			raw = append(raw, `,"id":7,"method":"tools/call","jsonrpc":"2.0"}`...)

			operation := func() {
				view, err := ScanProtocolObject(raw, limits)
				validationError, ok := err.(*ValidationError)
				if !ok || validationError.Kind() != KindResource || validationError.Scope() != ScopeProtocolParams {
					panic("unexpected protocol recovery result")
				}
				id, ok := view.Root().Value("id")
				if !ok || string(id.Bytes()) != "7" {
					panic("request id was not recovered")
				}
			}
			operation()
			bytesPerScan := measureAllocatedBytesPerRun(3, operation)
			ceiling := uint64(len(raw))*3 + (256 << 10)
			t.Logf("input=%d allocated_bytes_per_scan=%d ceiling=%d", len(raw), bytesPerScan, ceiling)
			if bytesPerScan > ceiling {
				t.Fatalf("allocated bytes per scan = %d, want <= %d", bytesPerScan, ceiling)
			}
		})
	}
}

func TestProtocolRecoveryAllocatedBytesStayBoundedAfterHeaderDuplicate(t *testing.T) {
	const repeats = 8_192
	var builder strings.Builder
	builder.Grow(repeats*7 + 64)
	builder.WriteString(`{"x":1,"x":2`)
	for range repeats {
		builder.WriteString(`,"id":0`)
	}
	builder.WriteString(`,"result":{}}`)
	raw := []byte(builder.String())
	limits := protocolTestLimits()

	operation := func() {
		view, err := ScanProtocolObject(raw, limits)
		validationError, ok := err.(*ValidationError)
		if !ok || validationError.Kind() != KindDuplicate || validationError.Scope() != ScopeProtocolEnvelope {
			panic("unexpected protocol recovery result")
		}
		if len(view.Root().Members()) != 3 {
			panic("recovery retained repeated headers")
		}
	}
	bytesPerScan := measureAllocatedBytesPerRun(3, operation)
	ceiling := uint64(len(raw))*3 + (256 << 10)
	t.Logf("input=%d allocated_bytes_per_scan=%d ceiling=%d", len(raw), bytesPerScan, ceiling)
	if bytesPerScan > ceiling {
		t.Fatalf("allocated bytes per scan = %d, want <= %d", bytesPerScan, ceiling)
	}
}

// measureAllocatedBytesPerRun reports the lowest of three fixed-size samples.
// The package does not run these tests in parallel; disabling automatic GC
// makes TotalAlloc deltas stable while explicit collections isolate samples.
func measureAllocatedBytesPerRun(runs int, operation func()) uint64 {
	if runs < 1 {
		panic("runs must be positive")
	}
	previousGCPercent := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGCPercent)

	operation()
	minimum := ^uint64(0)
	for sample := 0; sample < 3; sample++ {
		runtime.GC()
		var before runtime.MemStats
		var after runtime.MemStats
		runtime.ReadMemStats(&before)
		for run := 0; run < runs; run++ {
			operation()
		}
		runtime.ReadMemStats(&after)
		bytesPerRun := (after.TotalAlloc - before.TotalAlloc) / uint64(runs)
		if bytesPerRun < minimum {
			minimum = bytesPerRun
		}
	}
	return minimum
}

func nestedDuplicateObject(parentDepth int) []byte {
	var builder strings.Builder
	for depth := 0; depth < parentDepth; depth++ {
		builder.WriteString(`{"nested":`)
	}
	builder.WriteString(`{"x":1,"x":2}`)
	for depth := 0; depth < parentDepth; depth++ {
		builder.WriteByte('}')
	}
	return []byte(builder.String())
}

func validationErrorSignature(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("unexpected error type %T", err)
	}
	return string(validationError.Kind()) + ":" + strconv.Itoa(validationError.Position()) + ":" + string(validationError.Scope())
}

func assertFixedValidationError(t *testing.T, err error) {
	t.Helper()
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("unexpected error type %T", err)
	}
	want := "jsonwire: " + string(validationError.Kind())
	if err.Error() != want {
		t.Fatalf("error text = %q, want fixed %q", err, want)
	}
}
