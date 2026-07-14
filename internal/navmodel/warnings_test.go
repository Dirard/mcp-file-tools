package navmodel

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestAccumulator(t *testing.T) {
	var accumulator Accumulator
	if err := accumulator.Validate(); err != nil || !accumulator.Empty() {
		t.Fatalf("zero accumulator must be valid and empty: %v", err)
	}
	if err := accumulator.AddCandidate("b.go", api.WarningParserSkipped, api.WarningParserSkipped, api.WarningBinarySkipped); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.AddCandidate("a.go", api.WarningParserSkipped); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.AddCandidate(strings.Repeat("x", 129), api.WarningMountSkipped); err != nil {
		t.Fatal(err)
	}

	summaries := accumulator.Summaries()
	if len(summaries) != 3 {
		t.Fatalf("unexpected summaries: %+v", summaries)
	}
	if summaries[0].Code() != api.WarningBinarySkipped || summaries[0].Count() != 1 || summaries[0].Example() != "b.go" {
		t.Fatalf("unexpected first summary: %+v", summaries[0])
	}
	if summaries[1].Code() != api.WarningMountSkipped || summaries[1].Count() != 1 || summaries[1].Example() != "" {
		t.Fatalf("overlong example was retained: %+v", summaries[1])
	}
	if summaries[2].Code() != api.WarningParserSkipped || summaries[2].Count() != 2 || summaries[2].Example() != "b.go" {
		t.Fatalf("duplicate candidate code was counted or first example changed: %+v", summaries[2])
	}
	if accumulator.Validate() != nil || accumulator.Footprint() == 0 {
		t.Fatalf("valid accumulator rejected: %v", accumulator.Validate())
	}

	before := accumulator.Summaries()
	if err := accumulator.AddCandidate("secret", api.WarningCode("invalid")); err == nil {
		t.Fatal("invalid warning was accepted")
	}
	after := accumulator.Summaries()
	if len(after) != len(before) || after[2].Count() != before[2].Count() {
		t.Fatal("failed add mutated the accumulator")
	}
}

func TestAccumulatorAllCodes(t *testing.T) {
	var accumulator Accumulator
	codes := api.OrderedWarningCodes()
	input := make([]api.WarningCode, len(codes))
	copy(input, codes[:])
	if err := accumulator.AddCandidate("one.go", input...); err != nil {
		t.Fatal(err)
	}
	summaries := accumulator.Summaries()
	if len(summaries) != len(codes) {
		t.Fatalf("got %d summaries, want %d", len(summaries), len(codes))
	}
	for index, summary := range summaries {
		if index > 0 && string(summaries[index-1].Code()) >= string(summary.Code()) {
			t.Fatalf("summaries not ASCII sorted: %q then %q", summaries[index-1].Code(), summary.Code())
		}
		if summary.Count() != 1 || summary.Example() != "one.go" {
			t.Fatalf("unexpected summary: %+v", summary)
		}
	}
}
