package queue

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// toStreamValues mimics what Redis returns: every field comes back as a string.
func toStreamValues(m IngestMessage) map[string]any {
	out := map[string]any{}
	for k, v := range m.values() {
		switch x := v.(type) {
		case []byte:
			out[k] = string(x)
		default:
			out[k] = x
		}
	}
	return out
}

func TestIngestMessageRoundTrip(t *testing.T) {
	in := IngestMessage{
		ID: "0190a1b2-0000-7000-8000-000000000001", EndpointID: "e1", UserID: "u1", KeyVersion: 2,
		Ciphertext: []byte{0, 1, 2, 255}, Nonce: []byte("123456789012"),
		ReceivedAt: time.UnixMicro(1_700_000_000_123_456).UTC(), IngestMicros: 420,
		SourceIP: "203.0.113.9", SizeBytes: 1024, ContentType: "application/json", Method: "POST",
	}
	out, err := ParseIngestMessage(toStreamValues(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

func TestParseIngestMessageRejectsMalformed(t *testing.T) {
	good := toStreamValues(IngestMessage{ID: "x", EndpointID: "e", Nonce: []byte("n"), ReceivedAt: time.Now()})
	for _, drop := range []string{"id", "endpoint_id", "nonce", "received_at", "key_version"} {
		v := map[string]any{}
		for k, val := range good {
			if k != drop {
				v[k] = val
			}
		}
		if _, err := ParseIngestMessage(v); !errors.Is(err, ErrMalformed) {
			t.Errorf("missing %s: want ErrMalformed, got %v", drop, err)
		}
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	e := Envelope{Method: "POST", Query: "a=1", Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":true}`)}
	b, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalEnvelope(b)
	if err != nil || !reflect.DeepEqual(e, got) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if string(IngestAAD("abc")) != "ingest:abc" {
		t.Fatal("unexpected AAD")
	}
}

func TestDispatchMessageRoundTrip(t *testing.T) {
	in := DispatchMessage{LogID: "l1", EndpointID: "e1", Attempt: 3, PersistMicros: 812}
	v := map[string]any{}
	for k, x := range in.values() {
		v[k] = x
	}
	out, err := ParseDispatchMessage(v)
	if err != nil || out != in {
		t.Fatalf("got %+v, %v", out, err)
	}
	for _, bad := range []map[string]any{
		{"endpoint_id": "e", "attempt": "1", "persist_us": "0"},
		{"log_id": "l", "endpoint_id": "e", "attempt": "0", "persist_us": "0"},
		{"log_id": "l", "endpoint_id": "e", "attempt": "x", "persist_us": "0"},
	} {
		if _, err := ParseDispatchMessage(bad); !errors.Is(err, ErrMalformed) {
			t.Errorf("%v: want ErrMalformed, got %v", bad, err)
		}
	}
}
