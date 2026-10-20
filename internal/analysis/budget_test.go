package analysis

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

func TestAnalysisBudgetExactArithmetic(t *testing.T) {
	for _, c := range []struct {
		total, each string
		valid       bool
	}{
		{"0.3", "0.1", true}, {"0.00000001", "0.00000001", true}, {"999999999.99999999", "999999999.99999998", true},
		{"0", "0", false}, {"1", "0", false}, {"0.1", "0.10000001", false}, {"1e3", "1", false}, {"01", "1", false}, {"1.000000001", "1", false}, {"-1", "1", false}, {"1/2", "0.1", false}, {"1000000000", "1", false},
	} {
		if err := ValidateBudget(c.total, c.each); (err == nil) != c.valid {
			t.Fatalf("%s/%s: %v", c.total, c.each, err)
		}
	}
	expected := &pgconn.PgError{Code: "P0001", Message: "WORLD_ANALYSIS_BUDGET_EXHAUSTED"}
	if !errors.Is(budgetError(fmt.Errorf("wrapped: %w", expected)), ErrBudgetExhausted) {
		t.Fatal("budget error lost")
	}
	other := &pgconn.PgError{Code: "23505", Message: "WORLD_ANALYSIS_BUDGET_EXHAUSTED"}
	if budgetError(other) != other {
		t.Fatal("unrelated database failure changed")
	}
	q := &memoryQueue{}
	s := Scheduler{Queue: q, Advance: func(context.Context, Work) (string, error) { return "", ErrBudgetExhausted }}
	_, err := s.Tick(t.Context())
	if !errors.Is(err, ErrBudgetExhausted) || q.code != "budget_exhausted" || !q.released {
		t.Fatal("budget outcome lost", err)
	}
}
