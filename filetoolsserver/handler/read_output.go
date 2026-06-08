package handler

type ReadTextLine struct {
	Line int
	Text string
}

type readTextResult struct {
	lines     []ReadTextLine
	startLine int
	endLine   int
}

func intPtr(value int) *int {
	return &value
}

func lineRangePtr(start, end int) *LineRange {
	return &LineRange{Start: start, End: end}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
