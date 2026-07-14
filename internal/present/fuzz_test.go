package present

import (
	"testing"
	"unicode/utf8"
)

func FuzzBuilderPacking(f *testing.F) {
	f.Add("a.go", uint16(128))
	f.Add("tab\tcontrol\x01", uint16(256))
	f.Fuzz(func(t *testing.T, raw string, capSeed uint16) {
		if !utf8.ValidString(raw) {
			return
		}
		runes := []rune(raw)
		if len(runes) > 32 {
			runes = runes[:32]
		}
		path := string(runes)
		if path == "" {
			path = "."
		}
		unit, err := NewProjectUnit(ProjectFile, path)
		if err != nil {
			return
		}
		limit := uint64(64 + capSeed%512)
		left, err := newProjectBuilder(".", limit)
		if err != nil {
			t.Fatal(err)
		}
		right, err := newProjectBuilder(".", limit)
		if err != nil {
			t.Fatal(err)
		}
		leftFit := left.Try(unit)
		rightFit := right.Try(unit)
		if leftFit != rightFit {
			t.Fatalf("non-deterministic fit: %d != %d", leftFit, rightFit)
		}
		if leftFit != Fits {
			return
		}
		left.Commit(unit)
		right.Commit(unit)
		leftResult, leftErr := left.Finalize(Partial, readCursorPlaceholder, nil)
		rightResult, rightErr := right.Finalize(Partial, readCursorPlaceholder, nil)
		if (leftErr == nil) != (rightErr == nil) {
			t.Fatalf("non-deterministic finalize errors: %v / %v", leftErr, rightErr)
		}
		if leftErr != nil {
			return
		}
		leftText, _ := leftResult.Text()
		rightText, _ := rightResult.Text()
		if leftText != rightText || uint64(len(leftText)) > limit {
			t.Fatalf("non-deterministic or oversized output: %d/%d bytes", len(leftText), len(rightText))
		}
	})
}
