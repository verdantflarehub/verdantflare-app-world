package analysis

import (
	"errors"
	"math/big"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrBudgetExhausted = errors.New("analysis total budget exhausted")

func ValidateBudget(total, perCall string) error {
	if !positiveAmount.MatchString(total) || !positiveAmount.MatchString(perCall) {
		return ErrInvalid
	}
	a, ok := new(big.Rat).SetString(total)
	if !ok || a.Sign() <= 0 {
		return ErrInvalid
	}
	b, ok := new(big.Rat).SetString(perCall)
	if !ok || b.Sign() <= 0 || a.Cmp(b) < 0 {
		return ErrInvalid
	}
	return nil
}
func budgetError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "P0001" && pg.Message == "WORLD_ANALYSIS_BUDGET_EXHAUSTED" {
		return ErrBudgetExhausted
	}
	return err
}
