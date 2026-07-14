package navmodel

import (
	"errors"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
)

var errInvalidAccumulator = errors.New("invalid warning accumulator")

type warningSlot struct {
	count   uint64
	example string
}

type Accumulator struct {
	slots [12]warningSlot
}

type WarningSummary struct {
	code    api.WarningCode
	count   uint64
	example string
}

func (summary WarningSummary) Code() api.WarningCode {
	return summary.code
}

func (summary WarningSummary) Count() uint64 {
	return summary.count
}

func (summary WarningSummary) Example() string {
	return summary.example
}

func (accumulator *Accumulator) AddCandidate(path string, codes ...api.WarningCode) error {
	if accumulator == nil || accumulator.Validate() != nil {
		return errInvalidAccumulator
	}
	var selected [12]bool
	for _, code := range codes {
		index, ok := warningSlotIndex(code)
		if !ok {
			return errInvalidAccumulator
		}
		selected[index] = true
	}
	for index, add := range selected {
		if add && accumulator.slots[index].count == math.MaxUint64 {
			return errInvalidAccumulator
		}
	}

	retainExample := validWarningExample(path)
	for index, add := range selected {
		if !add {
			continue
		}
		slot := &accumulator.slots[index]
		slot.count++
		if slot.example == "" && retainExample {
			slot.example = strings.Clone(path)
		}
	}
	return nil
}

func (accumulator Accumulator) Summaries() []WarningSummary {
	codes := api.OrderedWarningCodes()
	summaries := make([]WarningSummary, 0, len(codes))
	for index, slot := range accumulator.slots {
		if slot.count == 0 {
			continue
		}
		summaries = append(summaries, WarningSummary{
			code:    codes[index],
			count:   slot.count,
			example: strings.Clone(slot.example),
		})
	}
	sort.Slice(summaries, func(left, right int) bool {
		return string(summaries[left].code) < string(summaries[right].code)
	})
	return summaries
}

func (accumulator Accumulator) Empty() bool {
	for _, slot := range accumulator.slots {
		if slot.count != 0 {
			return false
		}
	}
	return true
}

func (accumulator Accumulator) Validate() error {
	for _, slot := range accumulator.slots {
		if slot.count == 0 {
			if slot.example != "" {
				return errInvalidAccumulator
			}
			continue
		}
		if slot.example != "" && !validWarningExample(slot.example) {
			return errInvalidAccumulator
		}
	}
	return nil
}

func (accumulator Accumulator) Footprint() uint64 {
	bytes := uint64(unsafe.Sizeof(Accumulator{}))
	for _, slot := range accumulator.slots {
		bytes += uint64(len(slot.example))
	}
	return bytes
}

func warningSlotIndex(code api.WarningCode) (int, bool) {
	for index, candidate := range api.OrderedWarningCodes() {
		if code == candidate {
			return index, true
		}
	}
	return 0, false
}

func validWarningExample(path string) bool {
	return path != "" && uint64(len(path)) <= config.WarningSummaryLineMaxBytes &&
		len(path) <= api.InputStringMaxBytes && utf8.ValidString(path)
}
