package present

import (
	"strings"
	"testing"
)

const testCursor Cursor = "A7k3mP9qR2sT5uV8wX1yZw"

func TestRenderProject(t *testing.T) {
	page, err := RenderProject(ProjectPage{
		Path:   ".",
		Status: Complete,
		Entries: []ProjectEntry{
			{Kind: ProjectDirectory, Path: "."},
			{Kind: ProjectDirectory, Path: "dir"},
			{Kind: ProjectFile, Path: "dir"},
			{Kind: ProjectFile, Path: "z\t\n\\\x01.go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "@@project\t\".\"\tcomplete\trows=4\n" +
		"D\t\".\"\n" +
		"D\t\"dir\"\n" +
		"F\t\"dir\"\n" +
		"F\t\"z\\t\\n\\\\\\u0001.go\"\n"
	text, _ := page.Result.Text()
	if text != want || page.Rows != 4 || !page.Complete || page.Matches != 0 || page.Items != 0 || page.Result.IsError() || page.Result.Validate() != nil {
		t.Fatalf("unexpected project page:\n%s\npage=%+v resultErr=%v", text, page, page.Result.Validate())
	}

	partial, err := RenderProject(ProjectPage{
		Path:    "sub",
		Status:  Partial,
		Cursor:  testCursor,
		Entries: []ProjectEntry{{Kind: ProjectFile, Path: "sub/a.go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	partialText, _ := partial.Result.Text()
	if partialText != "@@project\t\"sub\"\tpartial\trows=1\tcursor="+string(testCursor)+"\nF\t\"sub/a.go\"\n" || partial.Complete {
		t.Fatalf("unexpected partial page: %q %+v", partialText, partial)
	}

	zero, err := RenderProject(ProjectPage{Path: ".", Status: Complete})
	if err != nil {
		t.Fatal(err)
	}
	zeroText, _ := zero.Result.Text()
	if zeroText != "@@project\t\".\"\tcomplete\trows=0\n" {
		t.Fatalf("unexpected zero page: %q", zeroText)
	}
}

func TestRenderProjectRejectsInvalidOrOversizedPage(t *testing.T) {
	invalid := []ProjectPage{
		{Path: "", Status: Complete},
		{Path: ".", Status: Complete, Cursor: testCursor},
		{Path: ".", Status: Partial},
		{Path: ".", Status: Complete, Entries: []ProjectEntry{{Kind: 0, Path: "a"}}},
		{Path: ".", Status: Complete, Entries: []ProjectEntry{{Kind: ProjectFile, Path: "b"}, {Kind: ProjectFile, Path: "a"}}},
		{Path: ".", Status: Complete, Entries: []ProjectEntry{{Kind: ProjectFile, Path: string([]byte{0xff})}}},
	}
	for index, input := range invalid {
		if _, err := RenderProject(input); err == nil {
			t.Fatalf("invalid project page %d was accepted", index)
		}
	}

	large := strings.Repeat("\x01", 4096)
	if _, err := RenderProject(ProjectPage{
		Path:   ".",
		Status: Complete,
		Entries: []ProjectEntry{
			{Kind: ProjectDirectory, Path: large},
			{Kind: ProjectFile, Path: large},
		},
	}); err == nil {
		t.Fatal("page above the 32768-byte hard cap was accepted")
	}
}
