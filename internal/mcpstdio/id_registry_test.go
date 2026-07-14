package mcpstdio

import (
	"strconv"
	"testing"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
)

func TestUsedIDRegistryHasExactFixedProductionAccounting(t *testing.T) {
	registry := newUsedIDRegistry()
	if got := len(registry.slots); got != int(config.UsedIDTableSlots) {
		t.Fatalf("slot count = %d, want %d", got, config.UsedIDTableSlots)
	}
	if got := unsafe.Sizeof(usedIDSlot{}); got != 40 {
		t.Fatalf("slot size = %d, want 40", got)
	}
	if got := cap(registry.arena); got != int(config.UsedIDArenaMaxBytes) {
		t.Fatalf("arena capacity = %d, want %d", got, config.UsedIDArenaMaxBytes)
	}
	if registry.maxRequests != uint32(config.SessionMaxRequests) {
		t.Fatalf("max requests = %d, want %d", registry.maxRequests, config.SessionMaxRequests)
	}
}

func TestUsedIDAdmissionRejectsActiveAndCompletedReuse(t *testing.T) {
	registry := newSmallUsedIDRegistry(defaultSemanticIDDigest)
	request := mustClassifyRequest(t, `1`, "ping", "")
	first := registry.admit(request)
	if first.kind != requestIDAccepted || first.output != "" || first.closeConnection {
		t.Fatalf("first admission = %#v", first)
	}

	for _, phase := range []string{"active", "completed"} {
		duplicate := registry.admit(request)
		want := `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid request"}}` + "\n"
		if duplicate.kind != requestIDDuplicate || duplicate.output != want || duplicate.closeConnection {
			t.Fatalf("%s duplicate admission = %#v", phase, duplicate)
		}
	}
}

func TestUsedIDAdmissionPrecedesParamsAndMethodValidation(t *testing.T) {
	registry := newSmallUsedIDRegistry(defaultSemanticIDDigest)
	paramsInvalid := mustClassifyRequest(t, `11`, "ping", `,"params":{"x":1,"x":2}`)
	if paramsInvalid.validationError == nil || paramsInvalid.validationError.Scope() != jsonwire.ScopeProtocolParams {
		t.Fatalf("params validation error = %v", paramsInvalid.validationError)
	}
	unknownMethod := mustClassifyRequest(t, `12`, "extension/unknown", "")

	for _, message := range []inboundMessage{paramsInvalid, unknownMethod} {
		if got := registry.admit(message); got.kind != requestIDAccepted {
			t.Fatalf("first admission = %#v", got)
		}
		if got := registry.admit(message); got.kind != requestIDDuplicate {
			t.Fatalf("reuse after downstream rejection = %#v", got)
		}
	}
}

func TestUsedIDRegistryUsesSemanticNumericEquality(t *testing.T) {
	registry := newSmallUsedIDRegistry(defaultSemanticIDDigest)
	first := registry.admit(mustClassifyRequest(t, `1e3`, "ping", ""))
	if first.kind != requestIDAccepted {
		t.Fatalf("first admission = %#v", first)
	}
	duplicate := registry.admit(mustClassifyRequest(t, `1000.0`, "ping", ""))
	want := `{"jsonrpc":"2.0","id":1000.0,"error":{"code":-32600,"message":"invalid request"}}` + "\n"
	if duplicate.kind != requestIDDuplicate || duplicate.output != want {
		t.Fatalf("equivalent numeric admission = %#v", duplicate)
	}
}

func TestUsedIDRegistryComparesFullKeysAfterInjectedDigestCollision(t *testing.T) {
	constantDigest := func(SemanticIDKey) [32]byte {
		return [32]byte{0x5a}
	}
	registry := newSmallUsedIDRegistry(constantDigest)
	alpha := mustClassifyRequest(t, `"alpha"`, "ping", "")
	beta := mustClassifyRequest(t, `"beta"`, "ping", "")
	if got := registry.admit(alpha); got.kind != requestIDAccepted {
		t.Fatalf("alpha admission = %#v", got)
	}
	if got := registry.admit(beta); got.kind != requestIDAccepted {
		t.Fatalf("beta collision admission = %#v", got)
	}
	if got := registry.admit(alpha); got.kind != requestIDDuplicate {
		t.Fatalf("alpha reuse after collision = %#v", got)
	}
	if got := registry.admit(beta); got.kind != requestIDDuplicate {
		t.Fatalf("beta reuse after collision = %#v", got)
	}
}

func TestUsedIDRegistryEnforcesArenaCapWithoutLosingDuplicateDetection(t *testing.T) {
	registry := newUsedIDRegistryWithConfig(usedIDRegistryConfig{
		maxRequests:   4,
		tableSlots:    8,
		arenaMaxBytes: 3,
	}, defaultSemanticIDDigest)
	first := SemanticIDKey{encoded: "s1"}
	if got := registry.register(first); got != idRegistrationNew {
		t.Fatalf("first registration = %d", got)
	}
	if got := registry.register(SemanticIDKey{encoded: "s2"}); got != idRegistrationExhausted {
		t.Fatalf("arena overflow registration = %d", got)
	}
	if got := registry.register(first); got != idRegistrationDuplicate {
		t.Fatalf("duplicate at arena cap = %d", got)
	}
}

func TestUsedIDRegistryAccepts65536AndClosesOn65537th(t *testing.T) {
	registry := newUsedIDRegistry()
	for index := 0; index < int(config.SessionMaxRequests); index++ {
		key := SemanticIDKey{encoded: "s" + strconv.Itoa(index)}
		if got := registry.register(key); got != idRegistrationNew {
			t.Fatalf("registration %d = %d", index, got)
		}
	}
	if registry.count != uint32(config.SessionMaxRequests) {
		t.Fatalf("registered count = %d", registry.count)
	}

	duplicate := registry.admit(mustClassifyRequest(t, `"0"`, "ping", ""))
	if duplicate.kind != requestIDDuplicate || duplicate.closeConnection {
		t.Fatalf("duplicate after saturation = %#v", duplicate)
	}
	exhausted := registry.admit(mustClassifyRequest(t, `"overflow"`, "ping", ""))
	want := `{"jsonrpc":"2.0","id":"overflow","error":{"code":-32000,"message":"session request limit exceeded"}}` + "\n"
	if exhausted.kind != requestIDExhausted || exhausted.output != want || !exhausted.closeConnection {
		t.Fatalf("65,537th admission = %#v", exhausted)
	}
}

func TestUsedIDAdmissionIgnoresNotifications(t *testing.T) {
	registry := newSmallUsedIDRegistry(defaultSemanticIDDigest)
	message, err := classifyInbound([]byte(`{"jsonrpc":"2.0","method":"ping"}`))
	if err != nil || message.kind != inboundNotification {
		t.Fatalf("notification classification = (%d, %v)", message.kind, err)
	}
	admission := registry.admit(message)
	if admission.kind != requestIDIgnored || admission.output != "" || admission.closeConnection {
		t.Fatalf("notification admission = %#v", admission)
	}
	if registry.count != 0 || len(registry.arena) != 0 {
		t.Fatalf("notification consumed registry state: count=%d arena=%d", registry.count, len(registry.arena))
	}
}

func newSmallUsedIDRegistry(digest semanticIDDigest) *usedIDRegistry {
	return newUsedIDRegistryWithConfig(usedIDRegistryConfig{
		maxRequests:   16,
		tableSlots:    32,
		arenaMaxBytes: 16 * int(config.UsedIDKeyMaxBytes),
	}, digest)
}

func mustClassifyRequest(t *testing.T, rawID, method, suffix string) inboundMessage {
	t.Helper()
	raw := []byte(`{"jsonrpc":"2.0","id":` + rawID + `,"method":"` + method + `"` + suffix + `}`)
	message, err := classifyInbound(raw)
	if err != nil || message.kind != inboundRequest {
		t.Fatalf("classifyInbound(%s) = (%d, %v)", raw, message.kind, err)
	}
	return message
}
