package results

// Property-based round-trip tests for the JSONL record types (Row,
// Transcript, TurnEvent, ToolEvent), following the in-house hand-rolled Go
// pattern (strategist-usa
// go-shard-runner/internal/workers/parse_csv_property_test.go): deterministic
// PRNG, bounded case count, properties documented with file:line refs, no new
// dependencies.
//
// Properties under test:
//
//	P1 JSON round-trip (results.go Row :19-50, Transcript :73-89,
//	   TurnEvent :56-70, ToolEvent :92-98): marshal → unmarshal → marshal is
//	   byte-stable, and unmarshal(marshal(x)) deep-equals x for the fields
//	   that survive the wire format. Byte-stability of marshal∘unmarshal∘
//	   marshal proves the writer is idempotent on its own output (a property
//	   results.jsonl consumers rely on when they re-emit rows).
//	P2 no-throw on hostile JSON (results.go :19-50): decoding arbitrary byte
//	   sequences into Row/Transcript either fails with an error or produces a
//	   struct — never panics. results.jsonl is written next to agent output
//	   and re-read by tooling; hostile bytes must not take down the reader.
//	P3 field preservation (results.go :19-50): for every field with a
//	   non-omitempty JSON tag, the decoded value equals the encoded value
//	   (typed: strings, ints, bools, float64 cents-scale, time.Time UTC).
//	   This guards against silent tag/type drift (e.g. a field renamed on
//	   write but not read, or seconds→milliseconds).
//
// Findings from a failing property are BUG REPORTS: do not weaken the
// property; report the minimal counterexample.

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/daemonclient"
)

const resultsPropSeed = 20261106

var hostileStrings = []string{
	"", " ", "\n", "\r\n", "\"", "\\", "\t\u0000", "\uFEFF",
	"\U0001F40D\u202Ertl", "</script>", "a/b/../../etc/passwd",
	"' OR '1'='1", strings.Repeat("x", 4096), "%s%v%#v", "${jndi:ldap://x}",
}

var verdictPool = []string{"pass", "fail", "error", "timeout", "", "PASS", "pass\u0000"}

func genRoutingObservation(rnd *rand.Rand) *daemonclient.RoutingObservation {
	if rnd.Intn(3) == 0 {
		return nil
	}
	return &daemonclient.RoutingObservation{
		SessionID:            hostileStrings[rnd.Intn(len(hostileStrings))],
		AgentID:              hostileStrings[rnd.Intn(len(hostileStrings))],
		Intent:               hostileStrings[rnd.Intn(len(hostileStrings))],
		ClassificationMethod: hostileStrings[rnd.Intn(len(hostileStrings))],
		TaskID:               hostileStrings[rnd.Intn(len(hostileStrings))],
		TurnNo:               rnd.Intn(10),
		ClassifierModel:      hostileStrings[rnd.Intn(len(hostileStrings))],
		InputHash:            hostileStrings[rnd.Intn(len(hostileStrings))],
		EvidenceStatus:       []string{"ok", "unavailable", ""}[rnd.Intn(3)],
		Association:          hostileStrings[rnd.Intn(len(hostileStrings))],
		Detail:               hostileStrings[rnd.Intn(len(hostileStrings))],
	}
}

func genTime(rnd *rand.Rand) time.Time {
	return time.Unix(rnd.Int63n(2_000_000_000), rnd.Int63n(1_000_000_000)).UTC()
}

func genRow(rnd *rand.Rand) Row {
	return Row{
		Routing:            genRoutingObservation(rnd),
		RoutingChecks:      genRoutingChecks(rnd),
		Suite:              hostileStrings[rnd.Intn(len(hostileStrings))],
		TaskID:             hostileStrings[rnd.Intn(len(hostileStrings))],
		Attempt:            rnd.Intn(5) + 1,
		Seed:               rnd.Int63(),
		Model:              hostileStrings[rnd.Intn(len(hostileStrings))],
		HFRevision:         hostileStrings[rnd.Intn(len(hostileStrings))],
		Verdict:            verdictPool[rnd.Intn(len(verdictPool))],
		Passed:             rnd.Intn(2) == 0,
		TokensIn:           rnd.Int63n(1_000_000),
		TokensOut:          rnd.Int63n(1_000_000),
		CostUSD:            float64(rnd.Int63n(1_000_000)) / 100, // cent-scale exact in float64
		WallSeconds:        float64(rnd.Int63n(600_000)) / 1000,  // millisecond grain
		AckSeconds:         float64(rnd.Int63n(60_000)) / 1000,
		TurnSeconds:        float64(rnd.Int63n(600_000)) / 1000,
		Checks:             genChecks(rnd),
		ErrorKind:          []string{"", "timeout", "auth", "crash"}[rnd.Intn(4)],
		ErrorDetail:        hostileStrings[rnd.Intn(len(hostileStrings))],
		AutoApproved:       rnd.Intn(2) == 0,
		DeterministicFetch: rnd.Intn(2) == 0,
		StartedAt:          genTime(rnd),
		WorktreeKept:       rnd.Intn(2) == 0,
		WorktreePath:       hostileStrings[rnd.Intn(len(hostileStrings))],
	}
}

func genRoutingChecks(rnd *rand.Rand) []RoutingCheck {
	n := rnd.Intn(4)
	if n == 0 {
		return nil
	}
	out := make([]RoutingCheck, n)
	for i := range out {
		out[i] = RoutingCheck{
			Check:  hostileStrings[rnd.Intn(len(hostileStrings))],
			Status: []string{"pass", "fail", "error", ""}[rnd.Intn(4)],
			Detail: hostileStrings[rnd.Intn(len(hostileStrings))],
		}
	}
	return out
}

func genChecks(rnd *rand.Rand) []any {
	n := rnd.Intn(3)
	if n == 0 {
		return nil
	}
	out := make([]any, n)
	for i := range out {
		switch rnd.Intn(3) {
		case 0:
			out[i] = map[string]any{"name": hostileStrings[rnd.Intn(len(hostileStrings))], "ok": rnd.Intn(2) == 0}
		case 1:
			out[i] = hostileStrings[rnd.Intn(len(hostileStrings))]
		default:
			// Numbers must be generated as float64: encoding/json decodes
			// JSON numbers into any as float64, so int64 cannot survive the
			// round-trip by construction (type-lossy field, not a bug).
			out[i] = float64(rnd.Int63n(1000))
		}
	}
	return out
}

func genTranscript(rnd *rand.Rand) Transcript {
	t := Transcript{
		Routing:              genRoutingObservation(rnd),
		RoutingChecks:        genRoutingChecks(rnd),
		Suite:                hostileStrings[rnd.Intn(len(hostileStrings))],
		TaskID:               hostileStrings[rnd.Intn(len(hostileStrings))],
		Attempt:              rnd.Intn(5) + 1,
		Seed:                 rnd.Int63(),
		Prompt:               hostileStrings[rnd.Intn(len(hostileStrings))],
		FinalReply:           hostileStrings[rnd.Intn(len(hostileStrings))],
		RoutedAgent:          hostileStrings[rnd.Intn(len(hostileStrings))],
		ClassificationMethod: hostileStrings[rnd.Intn(len(hostileStrings))],
		Error:                hostileStrings[rnd.Intn(len(hostileStrings))],
		StartedAt:            genTime(rnd),
		EndedAt:              genTime(rnd),
	}
	if n := rnd.Intn(4); n > 0 {
		for i := 0; i < n; i++ {
			t.Turns = append(t.Turns, TurnEvent{
				Message:  hostileStrings[rnd.Intn(len(hostileStrings))],
				DelayS:   rnd.Intn(600),
				SentAt:   genTime(rnd),
				PathUsed: []string{"steer", "followup", "chat", ""}[rnd.Intn(4)],
				Accepted: rnd.Intn(2) == 0,
				Error:    hostileStrings[rnd.Intn(len(hostileStrings))],
			})
		}
	}
	if n := rnd.Intn(4); n > 0 {
		for i := 0; i < n; i++ {
			raw, _ := json.Marshal(map[string]any{"tool": hostileStrings[rnd.Intn(len(hostileStrings))], "n": rnd.Intn(100)})
			t.ToolTrace = append(t.ToolTrace, ToolEvent{
				At:     genTime(rnd),
				Topic:  hostileStrings[rnd.Intn(len(hostileStrings))],
				Type:   hostileStrings[rnd.Intn(len(hostileStrings))],
				Source: hostileStrings[rnd.Intn(len(hostileStrings))],
				Raw:    raw,
			})
		}
	}
	return t
}

// TestRowJSONRoundTrip ports P1+P3 for Row: decode(encode(x)) == x and
// encode(decode(encode(x))) == encode(x) (writer idempotence on its own
// output — the property results.jsonl re-emitters depend on).
func TestRowJSONRoundTrip(t *testing.T) {
	rnd := rand.New(rand.NewSource(resultsPropSeed))
	const iters = 1000
	for i := 0; i < iters; i++ {
		row := genRow(rnd)
		b1, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("iter %d: marshal Row: %v", i, err)
		}
		var back Row
		if err := json.Unmarshal(b1, &back); err != nil {
			t.Fatalf("iter %d: unmarshal Row: %v\nbytes: %s", i, err, b1)
		}
		if !reflect.DeepEqual(row, back) {
			t.Fatalf("iter %d: Row round-trip mismatch\nwant: %+v\ngot:  %+v", i, row, back)
		}
		b2, err := json.Marshal(back)
		if err != nil {
			t.Fatalf("iter %d: re-marshal Row: %v", i, err)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("iter %d: Row marshal not idempotent\nfirst:  %s\nsecond: %s", i, b1, b2)
		}
	}
}

// TestTranscriptJSONRoundTrip ports P1+P3 for Transcript (nested TurnEvent
// and ToolEvent with json.RawMessage payloads).
func TestTranscriptJSONRoundTrip(t *testing.T) {
	rnd := rand.New(rand.NewSource(resultsPropSeed + 1))
	const iters = 1000
	for i := 0; i < iters; i++ {
		tr := genTranscript(rnd)
		b1, err := json.Marshal(tr)
		if err != nil {
			t.Fatalf("iter %d: marshal Transcript: %v", i, err)
		}
		var back Transcript
		if err := json.Unmarshal(b1, &back); err != nil {
			t.Fatalf("iter %d: unmarshal Transcript: %v\nbytes: %s", i, err, b1)
		}
		if !reflect.DeepEqual(tr, back) {
			t.Fatalf("iter %d: Transcript round-trip mismatch\nwant: %+v\ngot:  %+v", i, tr, back)
		}
		b2, err := json.Marshal(back)
		if err != nil {
			t.Fatalf("iter %d: re-marshal Transcript: %v", i, err)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("iter %d: Transcript marshal not idempotent\nfirst:  %s\nsecond: %s", i, b1, b2)
		}
	}
}

// TestDecodeRow_NoThrowOnArbitraryBytes ports P2: hostile bytes into Row and
// Transcript produce either an error or a struct, never a panic.
func TestDecodeRow_NoThrowOnArbitraryBytes(t *testing.T) {
	rnd := rand.New(rand.NewSource(resultsPropSeed + 2))

	// Real wire shape as a base, then hostile mutations layered on it.
	base, _ := json.Marshal(genRow(rand.New(rand.NewSource(1))))
	hostiles := [][]byte{
		nil, {}, []byte(""), []byte("null"), []byte("[]"), []byte("{"),
		[]byte(`{"task_id":123}`), []byte(`{"seed":"x"}`),
		[]byte(`{"started_at":"not-a-time"}`),
		[]byte(`{"wall_seconds":1e999}`), // float overflow
		[]byte(`{"checks":[[["deep"]]]}`),
		[]byte(`{"routing":null,"routing_checks":null}`),
		bytes.Repeat([]byte{0x00, 0xff}, 512),
		[]byte(`{"suite":"` + strings.Repeat("\\ud800", 16) + `"}`), // lone surrogates
		append(append([]byte(nil), base[:len(base)/2]...), []byte(`"TRUNCATED`)...),
	}
	for i := 0; i < 300; i++ {
		b := make([]byte, rnd.Intn(256))
		rnd.Read(b)
		hostiles = append(hostiles, b)
	}

	for i, input := range hostiles {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("input %d panicked: %v (bytes %q)", i, r, truncateStr(input))
				}
			}()
			var row Row
			_ = json.Unmarshal(input, &row)
			var tr Transcript
			_ = json.Unmarshal(input, &tr)
		}()
	}
}

func truncateStr(b []byte) string {
	s := string(b)
	if len(s) > 60 {
		s = s[:60] + "..."
	}
	return s
}
