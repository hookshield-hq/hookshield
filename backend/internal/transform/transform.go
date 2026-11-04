// Package transform implements HookShield's declarative payload pipeline: the
// rules stored in endpoints.transform_rules that strip, add, rename and format
// fields of the body and headers before delivery.
//
// Rules are data, not code: they are validated once, applied in order and can
// never execute arbitrary logic. The same Apply function powers the worker and
// the dashboard's live preview, so what users see is exactly what is sent.
//
// Format (version 1):
//
//	{
//	  "version": 1,
//	  "body": [
//	    {"op": "remove", "path": "data.card.number"},
//	    {"op": "rename", "from": "user_id", "to": "customerId"},
//	    {"op": "copy",   "from": "data.object.id", "to": "objectId"},
//	    {"op": "set",    "path": "source", "value": "stripe"},
//	    {"op": "set_timestamp", "path": "forwarded_at", "format": "rfc3339"},
//	    {"op": "keep",   "paths": ["id", "type", "objectId"]}
//	  ],
//	  "headers": [
//	    {"op": "remove", "name": "Stripe-Signature"},
//	    {"op": "rename", "from": "X-Request-Id", "to": "X-Upstream-Request-Id"},
//	    {"op": "set",    "name": "X-Source", "value": "stripe"}
//	  ]
//	}
//
// Paths use dots for object keys and numbers for array indexes ("items.0.sku").
// Body rules only apply to JSON object bodies; other bodies pass through
// unchanged. When there are no body rules the original bytes are forwarded
// untouched (no re-encoding).
package transform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Version is the only supported rules format version.
const Version = 1

// Limits that keep rules cheap to evaluate.
const (
	MaxRules     = 100
	MaxPathDepth = 32
	// CopyAllowance is how much copy rules may add on top of the input size
	// (bytes of JSON, estimated). Copies are the only rules that can grow a
	// body, and repeated copies of a node into itself would double it each time.
	CopyAllowance = 64 << 10
)

// ErrOutputTooLarge means copy rules would grow the body past the input size
// plus CopyAllowance. The rules are not applied.
var ErrOutputTooLarge = errors.New("transform.output_too_large")

// Body operations.
const (
	OpRemove       = "remove"
	OpRename       = "rename"
	OpCopy         = "copy"
	OpSet          = "set"
	OpSetTimestamp = "set_timestamp"
	OpKeep         = "keep"
)

// Timestamp formats for set_timestamp.
const (
	FormatRFC3339 = "rfc3339"
	FormatUnix    = "unix"
	FormatUnixMs  = "unix_ms"
)

// Rules is the parsed content of endpoints.transform_rules.
type Rules struct {
	Version int          `json:"version"`
	Body    []BodyRule   `json:"body,omitempty"`
	Headers []HeaderRule `json:"headers,omitempty"`
}

// BodyRule is one body operation.
type BodyRule struct {
	Op     string          `json:"op"`
	Path   string          `json:"path,omitempty"`
	From   string          `json:"from,omitempty"`
	To     string          `json:"to,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
	Format string          `json:"format,omitempty"`
	Paths  []string        `json:"paths,omitempty"`
}

// HeaderRule is one header operation (remove, rename or set).
type HeaderRule struct {
	Op    string `json:"op"`
	Name  string `json:"name,omitempty"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Value string `json:"value,omitempty"`
}

// Message is what the rules transform.
type Message struct {
	Headers map[string][]string
	Body    []byte
}

// ValidationError lists every problem found in a rules document. Each Code is a
// stable, translatable identifier (e.g. "transform.path_required") and Index
// points at the offending rule.
type ValidationError struct {
	Problems []Problem
}

// Problem is one validation finding.
type Problem struct {
	Section string `json:"section"` // "body", "headers" or "rules"
	Index   int    `json:"index"`
	Code    string `json:"code"`
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = fmt.Sprintf("%s[%d]: %s", p.Section, p.Index, p.Code)
	}
	return "transform: invalid rules: " + strings.Join(parts, "; ")
}

// Parse decodes and validates a rules document. Empty input ({} or null) yields
// empty rules.
func Parse(raw []byte) (Rules, error) {
	var r Rules
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" || string(trimmed) == "{}" {
		return Rules{Version: Version}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Rules{}, &ValidationError{Problems: []Problem{{Section: "rules", Code: "transform.invalid_json"}}}
	}
	if r.Version == 0 {
		r.Version = Version
	}
	return r, r.Validate()
}

// Empty reports whether the rules do nothing.
func (r Rules) Empty() bool { return len(r.Body) == 0 && len(r.Headers) == 0 }

// Validate checks every rule and returns a *ValidationError listing all problems.
func (r Rules) Validate() error {
	var ps []Problem
	add := func(sec string, i int, code string) { ps = append(ps, Problem{Section: sec, Index: i, Code: code}) }

	if r.Version != Version {
		add("rules", 0, "transform.unsupported_version")
	}
	if len(r.Body)+len(r.Headers) > MaxRules {
		add("rules", 0, "transform.too_many_rules")
	}

	for i, b := range r.Body {
		switch b.Op {
		case OpRemove:
			if !validPath(b.Path) {
				add("body", i, "transform.path_required")
			}
		case OpRename, OpCopy:
			if !validPath(b.From) || !validPath(b.To) {
				add("body", i, "transform.from_to_required")
			} else if b.To == b.From || strings.HasPrefix(b.To, b.From+".") {
				add("body", i, "transform.target_inside_source")
			}
		case OpSet:
			if !validPath(b.Path) {
				add("body", i, "transform.path_required")
			}
			if len(b.Value) == 0 || !json.Valid(b.Value) {
				add("body", i, "transform.value_required")
			}
		case OpSetTimestamp:
			if !validPath(b.Path) {
				add("body", i, "transform.path_required")
			}
			switch b.Format {
			case "", FormatRFC3339, FormatUnix, FormatUnixMs:
			default:
				add("body", i, "transform.invalid_format")
			}
		case OpKeep:
			if len(b.Paths) == 0 {
				add("body", i, "transform.paths_required")
			}
			for _, p := range b.Paths {
				if !validPath(p) {
					add("body", i, "transform.path_required")
					break
				}
			}
		default:
			add("body", i, "transform.unknown_op")
		}
	}

	for i, h := range r.Headers {
		switch h.Op {
		case OpRemove:
			if !validHeader(h.Name) {
				add("headers", i, "transform.header_name_required")
			}
		case OpRename:
			if !validHeader(h.From) || !validHeader(h.To) {
				add("headers", i, "transform.from_to_required")
			}
		case OpSet:
			if !validHeader(h.Name) {
				add("headers", i, "transform.header_name_required")
			}
			if strings.ContainsAny(h.Value, "\r\n") {
				add("headers", i, "transform.invalid_header_value")
			}
		default:
			add("headers", i, "transform.unknown_op")
		}
	}

	if len(ps) > 0 {
		return &ValidationError{Problems: ps}
	}
	return nil
}

func validPath(p string) bool {
	if p == "" {
		return false
	}
	segs := strings.Split(p, ".")
	if len(segs) > MaxPathDepth {
		return false
	}
	for _, s := range segs {
		if s == "" {
			return false
		}
	}
	return true
}

func validHeader(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		// RFC 9110 token characters.
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

// ErrNotJSONObject is reported (not returned) when body rules are skipped
// because the body is not a JSON object.
var ErrNotJSONObject = errors.New("transform: body is not a JSON object; body rules skipped")

// Result is the outcome of Apply.
type Result struct {
	Message Message
	// Warnings are non-fatal issues (e.g. body rules skipped for a non-JSON body,
	// a path that did not exist). They are shown in the debugger.
	Warnings []string
	// Err is ErrOutputTooLarge when the rules were not applied; Message is then
	// the input unchanged and must not be delivered as if transformed.
	Err error
}

// Apply runs the rules over m and returns the transformed copy; m is not modified.
// now is used by set_timestamp.
func (r Rules) Apply(m Message, now time.Time) Result {
	res := Result{Message: Message{Headers: applyHeaders(r.Headers, m.Headers), Body: m.Body}}
	if len(r.Body) == 0 {
		return res
	}

	dec := json.NewDecoder(bytes.NewReader(m.Body))
	dec.UseNumber() // keep big integers and decimals exactly as received
	var doc any
	obj, ok := map[string]any(nil), false
	if err := dec.Decode(&doc); err == nil {
		obj, ok = doc.(map[string]any)
	}
	if !ok || dec.More() {
		res.Warnings = append(res.Warnings, ErrNotJSONObject.Error())
		return res
	}

	budget := int64(len(m.Body)) + CopyAllowance
	for i, b := range r.Body {
		var warn string
		switch b.Op {
		case OpRemove:
			if !remove(obj, split(b.Path)) {
				warn = "path not found: " + b.Path
			}
		case OpRename:
			v, found := get(obj, split(b.From))
			if !found {
				warn = "path not found: " + b.From
				break
			}
			remove(obj, split(b.From))
			set(obj, split(b.To), v)
		case OpCopy:
			v, found := get(obj, split(b.From))
			if !found {
				warn = "path not found: " + b.From
				break
			}
			cp, ok := deepCopy(v, &budget)
			if !ok {
				return Result{Message: m, Warnings: res.Warnings, Err: ErrOutputTooLarge}
			}
			set(obj, split(b.To), cp)
		case OpSet:
			var v any
			vd := json.NewDecoder(bytes.NewReader(b.Value))
			vd.UseNumber()
			_ = vd.Decode(&v)
			set(obj, split(b.Path), v)
		case OpSetTimestamp:
			set(obj, split(b.Path), timestamp(now, b.Format))
		case OpKeep:
			kept := map[string]any{}
			for _, p := range b.Paths {
				if v, found := get(obj, split(p)); found {
					set(kept, split(p), v)
				}
			}
			for k := range obj {
				delete(obj, k)
			}
			for k, v := range kept {
				obj[k] = v
			}
		}
		if warn != "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("body[%d] %s", i, warn))
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err == nil {
		res.Message.Body = bytes.TrimRight(buf.Bytes(), "\n")
	}
	return res
}

func applyHeaders(rules []HeaderRule, in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in)+len(rules))
	for k, v := range in {
		out[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
	}
	for _, h := range rules {
		switch h.Op {
		case OpRemove:
			delete(out, http.CanonicalHeaderKey(h.Name))
		case OpRename:
			from, to := http.CanonicalHeaderKey(h.From), http.CanonicalHeaderKey(h.To)
			if v, ok := out[from]; ok {
				delete(out, from)
				out[to] = v
			}
		case OpSet:
			out[http.CanonicalHeaderKey(h.Name)] = []string{h.Value}
		}
	}
	return out
}

func timestamp(now time.Time, format string) any {
	switch format {
	case FormatUnix:
		return json.Number(strconv.FormatInt(now.Unix(), 10))
	case FormatUnixMs:
		return json.Number(strconv.FormatInt(now.UnixMilli(), 10))
	default:
		return now.UTC().Format(time.RFC3339Nano)
	}
}

func split(p string) []string { return strings.Split(p, ".") }

// get walks objects by key and arrays by numeric index.
func get(node any, path []string) (any, bool) {
	cur := node
	for _, seg := range path {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// set creates intermediate objects as needed. Array elements can be replaced
// but arrays are never grown; a non-container in the way is replaced by an object.
func set(root map[string]any, path []string, v any) {
	var cur any = root
	for i, seg := range path {
		last := i == len(path)-1
		switch c := cur.(type) {
		case map[string]any:
			if last {
				c[seg] = v
				return
			}
			next, ok := c[seg]
			if !ok || !isContainer(next) {
				next = map[string]any{}
				c[seg] = next
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(c) {
				return
			}
			if last {
				c[idx] = v
				return
			}
			if !isContainer(c[idx]) {
				c[idx] = map[string]any{}
			}
			cur = c[idx]
		default:
			return
		}
	}
}

func remove(root map[string]any, path []string) bool {
	parent, ok := get(root, path[:len(path)-1])
	if !ok {
		return false
	}
	last := path[len(path)-1]
	switch p := parent.(type) {
	case map[string]any:
		if _, ok := p[last]; !ok {
			return false
		}
		delete(p, last)
		return true
	case []any:
		i, err := strconv.Atoi(last)
		if err != nil || i < 0 || i >= len(p) {
			return false
		}
		p[i] = nil // arrays keep their length; removed elements become null
		return true
	}
	return false
}

func isContainer(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

// deepCopy copies v, spending its estimated JSON size from budget; false
// when the budget runs out (the copy is then incomplete and must be discarded).
func deepCopy(v any, budget *int64) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		*budget -= 2
		out := make(map[string]any, len(t))
		for k, x := range t {
			*budget -= int64(len(k)) + 4
			c, ok := deepCopy(x, budget)
			if !ok {
				return nil, false
			}
			out[k] = c
		}
		return out, *budget >= 0
	case []any:
		*budget -= 2 + int64(len(t))
		out := make([]any, len(t))
		for i, x := range t {
			c, ok := deepCopy(x, budget)
			if !ok {
				return nil, false
			}
			out[i] = c
		}
		return out, *budget >= 0
	case string:
		*budget -= int64(len(t)) + 2
	case json.Number:
		*budget -= int64(len(t))
	default:
		*budget -= 5 // true, false, null
	}
	return v, *budget >= 0
}
