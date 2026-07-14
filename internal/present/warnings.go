package present

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

type Warning struct {
	Code  api.WarningCode
	Count uint64
	Path  string
}

func WarningsFromAccumulator(accumulator navmodel.Accumulator) ([]Warning, error) {
	if accumulator.Validate() != nil {
		return nil, errInvalidPresentation
	}
	summaries := accumulator.Summaries()
	warnings := make([]Warning, len(summaries))
	for index, summary := range summaries {
		warnings[index] = Warning{
			Code:  summary.Code(),
			Count: summary.Count(),
			Path:  strings.Clone(summary.Example()),
		}
	}
	return warnings, nil
}

func normalizeWarnings(status Status, warnings []Warning) ([]Warning, error) {
	if status == Partial && len(warnings) != 0 {
		return nil, errInvalidPresentation
	}
	owned := make([]Warning, len(warnings))
	for index, warning := range warnings {
		if !warning.Code.Valid() || warning.Count == 0 {
			return nil, errInvalidPresentation
		}
		owned[index] = Warning{Code: warning.Code, Count: warning.Count, Path: strings.Clone(warning.Path)}
	}
	sort.Slice(owned, func(left, right int) bool {
		return string(owned[left].Code) < string(owned[right].Code)
	})
	for index := 1; index < len(owned); index++ {
		if owned[index-1].Code == owned[index].Code {
			return nil, errInvalidPresentation
		}
	}
	return owned, nil
}

func appendBroadWarningsBuffer(buffer *outputBuffer, warnings []Warning) error {
	var total uint64
	for _, warning := range warnings {
		line, err := renderBroadWarning(warning)
		if err != nil {
			return err
		}
		total += uint64(len(line))
		buffer.appendString(string(line))
	}
	if total > config.WarningSummaryMaxBytes {
		return errInvalidPresentation
	}
	return nil
}

func renderBroadWarning(warning Warning) ([]byte, error) {
	if !warning.Code.Valid() || warning.Count == 0 {
		return nil, errInvalidPresentation
	}

	pathless := appendWarningPrefix(nil, warning, warning.Count)
	pathless = append(pathless, '\n')
	if warning.Path == "" || !validPresentPath(warning.Path) {
		return pathless, nil
	}

	buffer := newOutputBuffer(config.WarningSummaryLineMaxBytes)
	buffer.appendString(string(appendWarningPrefix(nil, warning, warning.Count-1)))
	buffer.appendString("\tpath=")
	if err := buffer.quote(warning.Path); err != nil {
		return pathless, nil
	}
	buffer.appendByte('\n')
	withPath, err := buffer.finish()
	if err != nil {
		return pathless, nil
	}
	return withPath, nil
}

func appendWarningPrefix(dst []byte, warning Warning, omitted uint64) []byte {
	dst = append(dst, '!', '\t')
	dst = append(dst, string(warning.Code)...)
	dst = append(dst, "\tcount="...)
	dst = strconv.AppendUint(dst, warning.Count, 10)
	dst = append(dst, "\tomitted="...)
	dst = strconv.AppendUint(dst, omitted, 10)
	return dst
}
