package present

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestPlanReadSourceAndPagination(t *testing.T) {
	item, err := navmodel.NewReadSourceItem(0, "a.go", []navmodel.ReadLine{
		mustPresentationLine(t, 8, "first|value"),
		mustPresentationLine(t, 9, "second"),
	}, []api.WarningCode{api.WarningParserPartial})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := navmodel.NewReadSnapshot(navmodel.ReadSource, []navmodel.ReadItem{item})
	if err != nil {
		t.Fatal(err)
	}
	plan, code := PlanRead(snapshot, config.OutputMaxBytes, 10)
	if code != "" || plan.Footprint() == 0 {
		t.Fatalf("plan failed: code=%q footprint=%d", code, plan.Footprint())
	}
	page, err := plan.Render(0, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "@@read\tsource\tcomplete\titems=1\n" +
		"@\t\"a.go\"\titem=0\t8:9\tcomplete\n" +
		"8|first|value\n" +
		"!\tparser_partial\titem=0\n" +
		"9|second\n"
	assertReadPage(t, page, want, 2, 1, true, false)

	large, _ := navmodel.NewReadSourceItem(0, "large.txt", []navmodel.ReadLine{
		mustPresentationLine(t, 1, strings.Repeat("a", 50)),
		mustPresentationLine(t, 2, strings.Repeat("b", 50)),
	}, nil)
	largeSnapshot, _ := navmodel.NewReadSnapshot(navmodel.ReadSource, []navmodel.ReadItem{large})
	paged, code := PlanRead(largeSnapshot, 170, 2)
	if code != "" {
		t.Fatalf("pagination plan failed: %q", code)
	}
	if paged.PageCount() != 2 {
		t.Fatalf("got %d pages, want 2", paged.PageCount())
	}
	first, err := paged.Render(0, testCursor)
	if err != nil {
		t.Fatal(err)
	}
	firstText, _ := first.Result.Text()
	if !strings.Contains(firstText, "\t1:1\tpartial\n") || !strings.Contains(firstText, "1|"+strings.Repeat("a", 50)+"\n") || first.Complete || first.Rows != 1 {
		t.Fatalf("unexpected first read page: %q %+v", firstText, first)
	}
	second, err := paged.Render(1, "")
	if err != nil {
		t.Fatal(err)
	}
	secondText, _ := second.Result.Text()
	if !strings.Contains(secondText, "\t2:2\tcomplete\n") || !strings.Contains(secondText, "2|"+strings.Repeat("b", 50)+"\n") || !second.Complete || second.Rows != 1 {
		t.Fatalf("unexpected second read page: %q %+v", secondText, second)
	}
	if _, err := paged.Render(2, ""); err == nil {
		t.Fatal("out-of-range read page was rendered")
	}
	if _, err := paged.Render(0, "short"); err == nil {
		t.Fatal("partial read page accepted an invalid cursor")
	}
	if _, code := PlanRead(largeSnapshot, 170, 1); code != api.ErrorBudgetExceeded {
		t.Fatalf("max-page exhaustion returned %q", code)
	}
}

func TestPlanReadOutlineEmptyAndErrors(t *testing.T) {
	outline, err := navmodel.NewReadOutlineItem(0, "a.go", api.LanguageGo, []navmodel.Record{
		{Type: navmodel.Import, Range: navmodel.Range{Start: 1, End: 2}, Name: "fmt"},
		{Type: navmodel.Heading, Range: navmodel.Range{Start: 4, End: 6}, Depth: 1, Kind: api.KindSection, Name: "Section"},
		{Type: navmodel.Symbol, Range: navmodel.Range{Start: 8, End: 9}, Depth: 0, Kind: api.KindFunction, Name: "run"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := navmodel.NewReadSnapshot(navmodel.ReadOutline, []navmodel.ReadItem{outline})
	plan, code := PlanRead(snapshot, config.OutputMaxBytes, 1)
	if code != "" {
		t.Fatal(code)
	}
	page, err := plan.Render(0, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "@@read\toutline\tcomplete\titems=1\n" +
		"@\t\"a.go\"\titem=0\tgo\tcomplete\n" +
		"I\t1:2\t\"fmt\"\n" +
		"H\t1\t4:6\t\"Section\"\n" +
		"S\t0\t8:9\tfunction\t\"run\"\n"
	assertReadPage(t, page, want, 3, 1, true, false)

	empty, _ := navmodel.NewReadSourceEmptyItem(0, "empty.txt", nil)
	failure, _ := navmodel.NewReadErrorItem(navmodel.ReadSource, 1, api.ErrorBudgetExceeded, nil)
	later, _ := navmodel.NewReadSourceEmptyItem(2, "later.txt", nil)
	mixed, _ := navmodel.NewReadSnapshot(navmodel.ReadSource, []navmodel.ReadItem{empty, failure, later})
	mixedPlan, code := PlanRead(mixed, config.OutputMaxBytes, 1)
	if code != "" {
		t.Fatal(code)
	}
	mixedPage, err := mixedPlan.Render(0, "")
	if err != nil {
		t.Fatal(err)
	}
	mixedText, _ := mixedPage.Result.Text()
	if mixedPage.Result.IsError() || mixedPage.Items != 3 || mixedPage.Rows != 0 ||
		!strings.Contains(mixedText, "@\t\"empty.txt\"\titem=0\tempty\tcomplete\n") ||
		!strings.Contains(mixedText, "@\t\"<path-hidden>\"\titem=1\terror\tbudget_exceeded\n") ||
		!strings.Contains(mixedText, "@\t\"later.txt\"\titem=2\tempty\tcomplete\n") {
		t.Fatalf("unexpected mixed page: %q %+v", mixedText, mixedPage)
	}

	errorOnly, _ := navmodel.NewReadErrorItem(navmodel.ReadSource, 0, api.ErrorNotFound, nil)
	errorSnapshot, _ := navmodel.NewReadSnapshot(navmodel.ReadSource, []navmodel.ReadItem{errorOnly})
	errorPlan, code := PlanRead(errorSnapshot, config.OutputMaxBytes, 1)
	if code != "" {
		t.Fatal(code)
	}
	errorPage, err := errorPlan.Render(0, "")
	if err != nil || !errorPage.Result.IsError() {
		t.Fatalf("all-error terminal page must be isError: page=%+v err=%v", errorPage, err)
	}
}

func TestPlanReadRejectsInvalidOrIntrinsicOverflow(t *testing.T) {
	if _, code := PlanRead(navmodel.ReadSnapshot{}, config.OutputMaxBytes, 1); code != api.ErrorInvalidInput {
		t.Fatalf("zero snapshot returned %q", code)
	}
	item, _ := navmodel.NewReadSourceItem(0, "a", []navmodel.ReadLine{mustPresentationLine(t, 1, strings.Repeat("x", 200))}, nil)
	snapshot, _ := navmodel.NewReadSnapshot(navmodel.ReadSource, []navmodel.ReadItem{item})
	if _, code := PlanRead(snapshot, 100, 2); code != api.ErrorRecordExceedsBudget {
		t.Fatalf("intrinsic overflow returned %q", code)
	}
	if _, code := PlanRead(snapshot, 0, 2); code != api.ErrorInvalidInput {
		t.Fatalf("zero byte budget returned %q", code)
	}
	if _, code := PlanRead(snapshot, config.OutputMaxBytes, 0); code != api.ErrorInvalidInput {
		t.Fatalf("zero page budget returned %q", code)
	}
}

func mustPresentationLine(t *testing.T, number uint32, text string) navmodel.ReadLine {
	t.Helper()
	line, err := navmodel.NewReadLine(number, text)
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func assertReadPage(t *testing.T, page Page, want string, rows, items uint64, complete, isError bool) {
	t.Helper()
	text, ok := page.Result.Text()
	if !ok || text != want || page.Rows != rows || page.Matches != 0 || page.Items != items || page.Complete != complete || page.Result.IsError() != isError || page.Result.Validate() != nil {
		t.Fatalf("unexpected read page: text=%q rows=%d items=%d complete=%v isError=%v resultErr=%v", text, page.Rows, page.Items, page.Complete, page.Result.IsError(), page.Result.Validate())
	}
}
