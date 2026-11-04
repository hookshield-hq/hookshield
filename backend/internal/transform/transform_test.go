package transform

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 3, 15, 4, 5, 0, time.UTC)

func mustParse(t *testing.T, raw string) Rules {
	t.Helper()
	r, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r
}

func applyBody(t *testing.T, rules, body string) (map[string]any, Result) {
	t.Helper()
	res := mustParse(t, rules).Apply(Message{Body: []byte(body)}, now)
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(string(res.Message.Body)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("output is not JSON: %s (%v)", res.Message.Body, err)
	}
	return out, res
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestBodyOperations(t *testing.T) {
	cases := []struct {
		name  string
		rules string
		in    string
		want  string
	}{
		{"remove nested", `{"body":[{"op":"remove","path":"data.card.number"}]}`,
			`{"data":{"card":{"number":"4242","brand":"visa"}}}`, `{"data":{"card":{"brand":"visa"}}}`},
		{"rename blueprint example", `{"body":[{"op":"rename","from":"user_id","to":"customerId"}]}`,
			`{"user_id":42,"x":1}`, `{"customerId":42,"x":1}`},
		{"rename into new nested object", `{"body":[{"op":"rename","from":"a","to":"meta.b"}]}`,
			`{"a":"v"}`, `{"meta":{"b":"v"}}`},
		{"copy keeps source", `{"body":[{"op":"copy","from":"data.object.id","to":"objectId"}]}`,
			`{"data":{"object":{"id":"in_1"}}}`, `{"data":{"object":{"id":"in_1"}},"objectId":"in_1"}`},
		{"set string", `{"body":[{"op":"set","path":"source","value":"stripe"}]}`,
			`{"a":1}`, `{"a":1,"source":"stripe"}`},
		{"set object", `{"body":[{"op":"set","path":"meta","value":{"env":"prod","n":1}}]}`,
			`{}`, `{"meta":{"env":"prod","n":1}}`},
		{"set overwrites scalar in the way", `{"body":[{"op":"set","path":"a.b","value":true}]}`,
			`{"a":5}`, `{"a":{"b":true}}`},
		{"array index", `{"body":[{"op":"set","path":"items.1.qty","value":9}]}`,
			`{"items":[{"qty":1},{"qty":2}]}`, `{"items":[{"qty":1},{"qty":9}]}`},
		{"array out of range is ignored", `{"body":[{"op":"set","path":"items.5.qty","value":9}]}`,
			`{"items":[{"qty":1}]}`, `{"items":[{"qty":1}]}`},
		{"remove array element becomes null", `{"body":[{"op":"remove","path":"items.0"}]}`,
			`{"items":[1,2]}`, `{"items":[null,2]}`},
		{"keep allow-list", `{"body":[{"op":"keep","paths":["id","data.object.id"]}]}`,
			`{"id":"evt_1","secret":"x","data":{"object":{"id":"in_1","pii":"y"}}}`, `{"data":{"object":{"id":"in_1"}},"id":"evt_1"}`},
		{"rules run in order", `{"body":[{"op":"rename","from":"a","to":"b"},{"op":"copy","from":"b","to":"c"},{"op":"remove","path":"b"}]}`,
			`{"a":1}`, `{"c":1}`},
		{"big numbers preserved", `{"body":[{"op":"set","path":"x","value":1}]}`,
			`{"id":12345678901234567890,"amount":10.10}`, `{"amount":10.10,"id":12345678901234567890,"x":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := applyBody(t, tc.rules, tc.in)
			var want map[string]any
			dec := json.NewDecoder(strings.NewReader(tc.want))
			dec.UseNumber()
			_ = dec.Decode(&want)
			if !reflect.DeepEqual(out, want) {
				t.Fatalf("got %s, want %s", asJSON(out), tc.want)
			}
		})
	}
}

func TestBigNumbersAreNotRounded(t *testing.T) {
	res := mustParse(t, `{"body":[{"op":"set","path":"x","value":1}]}`).
		Apply(Message{Body: []byte(`{"id":12345678901234567890,"amount":10.10}`)}, now)
	if !strings.Contains(string(res.Message.Body), "12345678901234567890") || !strings.Contains(string(res.Message.Body), "10.10") {
		t.Fatalf("numbers changed: %s", res.Message.Body)
	}
}

func TestCopyIsDeep(t *testing.T) {
	out, _ := applyBody(t, `{"body":[{"op":"copy","from":"a","to":"b"},{"op":"set","path":"b.x","value":2}]}`, `{"a":{"x":1}}`)
	if asJSON(out["a"]) != `{"x":1}` || asJSON(out["b"]) != `{"x":2}` {
		t.Fatalf("copy must be deep (source unchanged): %s", asJSON(out))
	}
}

func TestSetTimestamp(t *testing.T) {
	out, _ := applyBody(t, `{"body":[
		{"op":"set_timestamp","path":"a"},
		{"op":"set_timestamp","path":"b","format":"unix"},
		{"op":"set_timestamp","path":"c","format":"unix_ms"}]}`, `{}`)
	if out["a"] != "2026-11-03T15:04:05Z" || out["b"].(json.Number).String() != "1793718245" || out["c"].(json.Number).String() != "1793718245000" {
		t.Fatalf("unexpected timestamps: %s", asJSON(out))
	}
}

func TestWarnings(t *testing.T) {
	_, res := applyBody(t, `{"body":[{"op":"remove","path":"missing"},{"op":"rename","from":"nope","to":"x"}]}`, `{"a":1}`)
	if len(res.Warnings) != 2 || !strings.Contains(res.Warnings[0], "missing") {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}

func TestNonJSONBodyPassesThrough(t *testing.T) {
	r := mustParse(t, `{"body":[{"op":"set","path":"x","value":1}]}`)
	for _, body := range []string{"a=1&b=2", `[1,2,3]`, `"str"`, `{"a":1} {"b":2}`, ``} {
		res := r.Apply(Message{Body: []byte(body)}, now)
		if string(res.Message.Body) != body {
			t.Errorf("%q changed to %q", body, res.Message.Body)
		}
		if len(res.Warnings) != 1 {
			t.Errorf("%q: expected a not-JSON warning, got %v", body, res.Warnings)
		}
	}
}

func TestNoBodyRulesKeepsBytesExactly(t *testing.T) {
	body := `{ "b":2,  "a":1 }` // unusual spacing and key order must survive
	res := mustParse(t, `{"headers":[{"op":"set","name":"X-A","value":"1"}]}`).Apply(Message{Body: []byte(body)}, now)
	if string(res.Message.Body) != body {
		t.Fatalf("body re-encoded: %q", res.Message.Body)
	}
}

func TestHeaderOperations(t *testing.T) {
	r := mustParse(t, `{"headers":[
		{"op":"remove","name":"stripe-signature"},
		{"op":"rename","from":"X-Request-Id","to":"x-upstream-request-id"},
		{"op":"set","name":"x-source","value":"stripe"},
		{"op":"rename","from":"X-Absent","to":"X-Other"}]}`)
	in := map[string][]string{
		"Stripe-Signature": {"t=1"},
		"X-Request-Id":     {"abc"},
		"Content-Type":     {"application/json"},
	}
	res := r.Apply(Message{Headers: in}, now)
	want := map[string][]string{
		"X-Upstream-Request-Id": {"abc"},
		"Content-Type":          {"application/json"},
		"X-Source":              {"stripe"},
	}
	if !reflect.DeepEqual(res.Message.Headers, want) {
		t.Fatalf("got %v", res.Message.Headers)
	}
	if _, ok := in["X-Source"]; ok || len(in) != 3 {
		t.Fatal("input headers were mutated")
	}
}

func TestApplyDoesNotMutateInputBody(t *testing.T) {
	body := []byte(`{"a":1}`)
	mustParse(t, `{"body":[{"op":"remove","path":"a"}]}`).Apply(Message{Body: body}, now)
	if string(body) != `{"a":1}` {
		t.Fatal("input body mutated")
	}
}

func TestParseEmpty(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", "  {}  "} {
		r, err := Parse([]byte(raw))
		if err != nil || !r.Empty() || r.Version != Version {
			t.Errorf("%q: %+v %v", raw, r, err)
		}
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]struct {
		raw   string
		codes []string
	}{
		"invalid json":      {`{"body":`, []string{"transform.invalid_json"}},
		"unknown field":     {`{"bdy":[]}`, []string{"transform.invalid_json"}},
		"bad version":       {`{"version":2}`, []string{"transform.unsupported_version"}},
		"unknown body op":   {`{"body":[{"op":"eval"}]}`, []string{"transform.unknown_op"}},
		"remove no path":    {`{"body":[{"op":"remove"}]}`, []string{"transform.path_required"}},
		"empty path seg":    {`{"body":[{"op":"remove","path":"a..b"}]}`, []string{"transform.path_required"}},
		"rename no to":      {`{"body":[{"op":"rename","from":"a"}]}`, []string{"transform.from_to_required"}},
		"set no value":      {`{"body":[{"op":"set","path":"a"}]}`, []string{"transform.value_required"}},
		"bad ts format":     {`{"body":[{"op":"set_timestamp","path":"a","format":"iso"}]}`, []string{"transform.invalid_format"}},
		"keep empty":        {`{"body":[{"op":"keep","paths":[]}]}`, []string{"transform.paths_required"}},
		"header bad name":   {`{"headers":[{"op":"set","name":"Bad Name","value":"x"}]}`, []string{"transform.header_name_required"}},
		"header injection":  {`{"headers":[{"op":"set","name":"X-A","value":"a\r\nEvil: 1"}]}`, []string{"transform.invalid_header_value"}},
		"unknown header op": {`{"headers":[{"op":"copy","name":"X"}]}`, []string{"transform.unknown_op"}},
		"collects all": {`{"body":[{"op":"remove"},{"op":"nope"}],"headers":[{"op":"remove"}]}`,
			[]string{"transform.path_required", "transform.unknown_op", "transform.header_name_required"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.raw))
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want ValidationError, got %v", err)
			}
			var got []string
			for _, p := range ve.Problems {
				got = append(got, p.Code)
			}
			if !reflect.DeepEqual(got, tc.codes) {
				t.Fatalf("codes %v, want %v", got, tc.codes)
			}
		})
	}
}

func TestTooManyRules(t *testing.T) {
	rules := make([]string, MaxRules+1)
	for i := range rules {
		rules[i] = `{"op":"remove","path":"a"}`
	}
	_, err := Parse([]byte(`{"body":[` + strings.Join(rules, ",") + `]}`))
	if err == nil || !strings.Contains(err.Error(), "transform.too_many_rules") {
		t.Fatalf("got %v", err)
	}
}

func BenchmarkApply(b *testing.B) {
	r, _ := Parse([]byte(`{"body":[{"op":"rename","from":"user_id","to":"customerId"},{"op":"remove","path":"data.card"},{"op":"set_timestamp","path":"ts"}]}`))
	msg := Message{Body: []byte(`{"user_id":1,"type":"invoice.paid","data":{"card":{"n":"4242"},"amount":100,"items":[1,2,3]}}`)}
	for b.Loop() {
		r.Apply(msg, now)
	}
}

// Regression (security review): copying a node into its own subtree doubled it
// on every rule — 22 rules of ~40 bytes produced a 226 MB body and could take
// down the API (preview) and every worker.
func TestCopiesCannotBlowUpTheBody(t *testing.T) {
	// Copying into itself is rejected when the rules are saved.
	err := Rules{Version: Version, Body: []BodyRule{{Op: OpCopy, From: "a", To: "a.b"}}}.Validate()
	if err == nil || !strings.Contains(err.Error(), "transform.target_inside_source") {
		t.Fatalf("copy into own subtree: %v", err)
	}
	if err := (Rules{Version: Version, Body: []BodyRule{{Op: OpRename, From: "a", To: "a"}}}).Validate(); err == nil {
		t.Fatal("rename onto itself should be rejected")
	}
	if err := (Rules{Version: Version, Body: []BodyRule{{Op: OpCopy, From: "a", To: "ab"}}}).Validate(); err != nil {
		t.Fatalf("a sibling with a common prefix is fine: %v", err)
	}

	// Alternating copies grow like Fibonacci without ever copying into themselves:
	// the size budget stops them.
	var rules Rules
	rules.Version = Version
	for i := 0; i < 60; i++ {
		if i%2 == 0 {
			rules.Body = append(rules.Body, BodyRule{Op: OpCopy, From: "a", To: fmt.Sprintf("b.n%d", i)})
		} else {
			rules.Body = append(rules.Body, BodyRule{Op: OpCopy, From: "b", To: fmt.Sprintf("a.n%d", i)})
		}
	}
	if err := rules.Validate(); err != nil {
		t.Fatal(err)
	}
	in := Message{Body: []byte(`{"a":{"x":"` + strings.Repeat("y", 100) + `"},"b":{}}`)}
	start := time.Now()
	res := rules.Apply(in, time.Now())
	if !errors.Is(res.Err, ErrOutputTooLarge) || string(res.Message.Body) != string(in.Body) {
		t.Fatalf("want ErrOutputTooLarge and the input unchanged, got err=%v body=%d bytes", res.Err, len(res.Message.Body))
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("stopping took %s", d)
	}

	// Legitimate copies of a big body still work: one full copy fits the budget.
	big := `{"data":{"blob":"` + strings.Repeat("z", 500_000) + `"}}`
	res = mustParse(t, `{"version":1,"body":[{"op":"copy","from":"data","to":"backup"}]}`).Apply(Message{Body: []byte(big)}, time.Now())
	if res.Err != nil || len(res.Message.Body) < 1_000_000 {
		t.Fatalf("one copy of a 500 KB field: err=%v size=%d", res.Err, len(res.Message.Body))
	}
}
