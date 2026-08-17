package present

import (
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestError(t *testing.T) {
	for _, code := range api.OrderedErrorCodes() {
		result := Error(code)
		text, ok := result.Text()
		want := "ERROR\t" + string(code) + "\n"
		if code == api.ErrorInvalidInput {
			want = "ERROR\tinvalid_input\tfield=arguments\treason=does_not_match_tool_contract\n"
		}
		if !ok || text != want || !result.IsError() || result.Validate() != nil {
			t.Fatalf("invalid result for %q: text=%q ok=%v isError=%v err=%v", code, text, ok, result.IsError(), result.Validate())
		}
	}

	result := Error(api.ErrorCode("sentinel\tsecret\n"))
	text, _ := result.Text()
	if text != "ERROR\tinvalid_input\tfield=arguments\treason=does_not_match_tool_contract\n" || !result.IsError() || result.Validate() != nil {
		t.Fatalf("invalid code was reflected or produced an invalid result: %q err=%v", text, result.Validate())
	}
}
