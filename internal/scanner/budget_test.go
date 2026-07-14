package scanner

import "testing"

func TestCumulativeBudgetsNeverWrapOrReset(t *testing.T) {
	t.Parallel()

	state := &State{limits: Limits{MaxFiles: 1, MaxDirs: 1, MaxBytes: 10, MaxParserBytes: 4}}
	if !state.incrementDir() || state.incrementDir() {
		t.Fatal("directory limit was not cumulative")
	}
	if !state.incrementFile() || state.incrementFile() {
		t.Fatal("file limit was not cumulative")
	}
	if !state.chargeDirectory(6) || !state.chargeContent(4) || state.chargeContent(1) {
		t.Fatalf("scan byte counters = %#v", state.counters)
	}
	if !state.chargeParser(4) || state.chargeParser(1) {
		t.Fatalf("parser byte counter = %d", state.counters.ParserBytes)
	}
}
