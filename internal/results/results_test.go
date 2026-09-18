package results

import (
	"encoding/json"
	"github.com/bhodgens/meept-bench/internal/daemonclient"
	"testing"
)

func TestRoutingJSONCompatibility(t *testing.T) {
	var old Row
	if err := json.Unmarshal([]byte(`{"suite":"old","verdict":"pass","passed":true}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Routing != nil || len(old.RoutingChecks) != 0 {
		t.Fatal("legacy row must not imply route evidence")
	}
	var tr Transcript
	if err := json.Unmarshal([]byte(`{"final_reply":"ok","routed_agent":"coder"}`), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.Routing != nil {
		t.Fatal("legacy transcript must not imply route association")
	}
	row := Row{Routing: &daemonclient.RoutingObservation{EvidenceStatus: "unavailable"}, RoutingChecks: []RoutingCheck{{Check: "expect_agent", Status: "error", Detail: "no evidence"}}}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var round Row
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatal(err)
	}
	if round.Routing.EvidenceStatus != "unavailable" || round.RoutingChecks[0].Status != "error" {
		t.Fatalf("lost fields: %s", b)
	}
}
