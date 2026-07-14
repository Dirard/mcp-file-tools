package jsonwire

import (
	"errors"
	"testing"
)

func TestScanObjectProvidesExactOrderedMembers(t *testing.T) {
	raw := []byte(` {"method":"tools\/call","id":1e0,"params":{},"_meta":{}} `)
	view, err := ScanObject(raw, protocolTestLimits(), ValidateAll)
	if err != nil {
		t.Fatalf("ScanObject() error = %v", err)
	}

	members := view.Members()
	if len(members) != 4 {
		t.Fatalf("member count = %d, want 4", len(members))
	}
	wantKeys := []string{"method", "id", "params", "_meta"}
	wantKinds := []ValueKind{String, Number, Object, Object}
	wantValues := []string{`"tools\/call"`, `1e0`, `{}`, `{}`}
	for index := range members {
		member := members[index]
		if member.Key != wantKeys[index] || member.Kind != wantKinds[index] {
			t.Fatalf("member %d = (%q, %d), want (%q, %d)", index, member.Key, member.Kind, wantKeys[index], wantKinds[index])
		}
		if got := string(view.raw[member.KeySpan.Start:member.KeySpan.End]); got != `"`+wantKeys[index]+`"` {
			t.Fatalf("member %d key span = %q", index, got)
		}
		if got := string(view.raw[member.Value.Start:member.Value.End]); got != wantValues[index] {
			t.Fatalf("member %d value span = %q, want %q", index, got, wantValues[index])
		}
	}

	method, ok := view.Member("method")
	if !ok || method != members[0] {
		t.Fatalf("Member(method) = (%#v, %t), want first member", method, ok)
	}
	if missing, ok := view.Member("missing"); ok || missing != (Member{}) {
		t.Fatalf("Member(missing) = (%#v, %t), want zero,false", missing, ok)
	}
}

func TestObjectViewOwnsInputAndDefendsMemberSlice(t *testing.T) {
	raw := []byte(`{"key":{"nested":true}}`)
	view, err := ScanObject(raw, protocolTestLimits(), ValidateAll)
	if err != nil {
		t.Fatalf("ScanObject() error = %v", err)
	}
	for index := range raw {
		raw[index] = 'x'
	}

	member, ok := view.Member("key")
	if !ok || string(view.raw[member.Value.Start:member.Value.End]) != `{"nested":true}` {
		t.Fatalf("owned member changed after caller mutation: (%#v, %t)", member, ok)
	}
	members := view.Members()
	members[0].Key = "changed"
	members[0].Value = Span{}
	member, ok = view.Member("key")
	if !ok || member.Key != "key" || member.Value == (Span{}) {
		t.Fatalf("view changed after Members mutation: (%#v, %t)", member, ok)
	}
}

func TestScanObjectExtractsExactProtocolSubspans(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":1e0,"method":"tools\/call","params":{"name":"read","arguments":{"path":"a"},"_meta":{"progressToken":"p"},"task":{"ttl":60000}}}`)
	outer, err := ScanObject(raw, protocolTestLimits(), ProtocolWithRawArguments)
	if err != nil {
		t.Fatalf("outer ScanObject() error = %v", err)
	}
	requireRawMember(t, outer, "id", `1e0`, Number)
	requireRawMember(t, outer, "method", `"tools\/call"`, String)
	paramsMember := requireRawMember(t, outer, "params", `{"name":"read","arguments":{"path":"a"},"_meta":{"progressToken":"p"},"task":{"ttl":60000}}`, Object)

	params, err := ScanObject(outer.raw[paramsMember.Value.Start:paramsMember.Value.End], protocolTestLimits(), ValidateAll)
	if err != nil {
		t.Fatalf("params ScanObject() error = %v", err)
	}
	requireRawMember(t, params, "arguments", `{"path":"a"}`, Object)
	requireRawMember(t, params, "_meta", `{"progressToken":"p"}`, Object)
	requireRawMember(t, params, "task", `{"ttl":60000}`, Object)
}

func TestScanObjectHandlesOmittedMembersAndRejectsOtherKinds(t *testing.T) {
	view, err := ScanObject([]byte(`{}`), protocolTestLimits(), ValidateAll)
	if err != nil {
		t.Fatalf("ScanObject({}) error = %v", err)
	}
	if len(view.Members()) != 0 {
		t.Fatalf("empty object members = %#v", view.Members())
	}
	if _, ok := view.Member("arguments"); ok {
		t.Fatal("omitted arguments reported present")
	}

	_, err = ScanObject([]byte(`[]`), protocolTestLimits(), ValidateAll)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Kind() != KindMismatch {
		t.Fatalf("ScanObject([]) error = %v, want kind mismatch", err)
	}
}

func requireRawMember(t *testing.T, view ObjectView, name, wantRaw string, wantKind ValueKind) Member {
	t.Helper()
	member, ok := view.Member(name)
	if !ok {
		t.Fatalf("member %q missing", name)
	}
	if member.Kind != wantKind {
		t.Fatalf("member %q kind = %d, want %d", name, member.Kind, wantKind)
	}
	if got := string(view.raw[member.Value.Start:member.Value.End]); got != wantRaw {
		t.Fatalf("member %q raw = %q, want %q", name, got, wantRaw)
	}
	return member
}
