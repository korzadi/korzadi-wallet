package handlers

import (
	"errors"

	"github.com/shopspring/decimal"
)

const maxTransactionAmount = "1000000"
const maxDecimalPlaces = 8

var errAmountTooLarge = errors.New("el monto excede el máximo permitido por operación")
var errAmountTooManyDecimals = errors.New("el monto tiene demasiados decimales")

func parseAndValidateAmount(raw string) (decimal.Decimal, error) {
	amount, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, err
	}
	if amount.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, errors.New("el monto debe ser mayor a cero")
	}
	max, _ := decimal.NewFromString(maxTransactionAmount)
	if amount.GreaterThan(max) {
		return decimal.Zero, errAmountTooLarge
	}
	if amount.Exponent() < -maxDecimalPlaces {
		return decimal.Zero, errAmountTooManyDecimals
	}
	return amount, nil
}
