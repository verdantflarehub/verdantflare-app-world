package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func oid(n string) string { return "0192f3d4-0000-7000-8000-00000000000" + n }
func operation() Operation {
	return Operation{"project", oid("1"), oid("2"), oid("3"), oid("4"), strings.Repeat("a", 64), 1}
}

func TestTerminalProofRejectsUnknownAndMismatchedIdentity(t *testing.T) {
	o := operation()
	for _, state := range []string{"", "preparing", "prepared", "failed", "expired"} {
		if VerifyTerminal(o, Status{o, state}) == nil {
			t.Fatalf("unsafe terminal state %q accepted", state)
		}
	}
	for _, state := range []string{"committed", "cancelled"} {
		if err := VerifyTerminal(o, Status{o, state}); err != nil {
			t.Fatal(err)
		}
	}
	variants := []Operation{o, o, o, o, o}
	variants[0].Fence++
	variants[1].SubjectID = oid("5")
	variants[2].RequestHash = strings.Repeat("b", 64)
	variants[3].Coordinator = "world"
	variants[4].CommitID = oid("6")
	for _, other := range variants {
		if VerifyTerminal(o, Status{other, "cancelled"}) == nil {
			t.Fatal("foreign terminal outcome released a reservation")
		}
	}
}

func TestPlanOrdersResourcesAndPreservesStrongestPermission(t *testing.T) {
	p, err := Normalize(Plan{"project": {{oid("5"), false}, {oid("3"), true}, {oid("5"), true}}, "world": {{oid("2"), false}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p["project"]) != 2 || p["project"][0].ResourceID != oid("3") || !p["project"][1].Write {
		t.Fatal("grant ordering or write requirement lost")
	}
	for _, bad := range []Plan{{"untrusted": {{oid("1"), false}}}, {"world": {}}, {"world": {{"latest", false}}}} {
		if _, err = Normalize(bad); err == nil {
			t.Fatal("invalid participant or grant accepted")
		}
	}
}

func TestRPCRejectsCallerOutcomeAndWrongCoordinatorWithoutDatabase(t *testing.T) {
	e := &Engine{}
	h, err := e.Handler(map[string]string{"world": strings.Repeat("w", 32)})
	if err != nil {
		t.Fatal(err)
	}
	o := operation()
	raw, _ := json.Marshal(o)
	for _, body := range []string{`{"operation":` + string(raw) + `,"outcome":"committed"}`, `{"operation":` + string(raw) + `}`} {
		r := httptest.NewRequest("POST", "/internal/v1/coordination/finalize", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("w", 32))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 && w.Code != 403 {
			t.Fatal("untrusted finalization reached store", w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/internal/v1/coordination/state", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing service credential accepted")
	}
}

func TestPeerConfigurationDoesNotAcceptArbitraryRemotePlaintextOrRedirectTargets(t *testing.T) {
	for _, origin := range []string{"http://peer.example.invalid", "https://peer.example.invalid/path", "https://u:p@peer.example.invalid", "https://peer.example.invalid?redirect=1", "file:///tmp/socket"} {
		if _, err := NewClient(origin, strings.Repeat("t", 32)); err == nil {
			t.Fatal("unsafe peer origin accepted")
		}
	}
	if _, err := NewClient("https://world.example.invalid", strings.Repeat("t", 32)); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeCannotTreatMissingCoordinatorAsProof(t *testing.T) {
	o := operation()
	e := &Engine{Store: &Store{Domain: "world"}, Peers: map[string]Peer{}}
	if err := e.Finalize(context.Background(), o); !errors.Is(err, ErrUnknown) {
		t.Fatal("missing authoritative outcome was not retained", err)
	}
}
