// Package mortgage считает аннуитетный ипотечный платёж.
package mortgage

import (
	"errors"
	"math"
)

// Result — итог расчёта ипотеки.
type Result struct {
	Price        float64 // стоимость объекта, ₽
	Down         float64 // первоначальный взнос, ₽
	Loan         float64 // сумма кредита, ₽
	Rate         float64 // ставка, % годовых
	Years        int     // срок, лет
	Monthly      float64 // ежемесячный платёж, ₽
	Total        float64 // всего выплат по кредиту, ₽
	Overpay      float64 // переплата (проценты), ₽
	IncomeNeeded float64 // ориентировочный доход: платёж не больше 50% дохода
}

// Monthly — аннуитетный платёж: S · r(1+r)^n / ((1+r)^n − 1), r = ставка/12.
func Monthly(loan, annualRate float64, months int) float64 {
	if months <= 0 || loan <= 0 {
		return 0
	}
	if annualRate == 0 {
		return loan / float64(months)
	}
	r := annualRate / 100 / 12
	k := math.Pow(1+r, float64(months))
	return loan * r * k / (k - 1)
}

// Calculate проверяет входные данные и считает итог.
func Calculate(price, down, rate float64, years int) (Result, error) {
	switch {
	case price <= 0:
		return Result{}, errors.New("стоимость должна быть больше нуля")
	case down < 0 || down >= price:
		return Result{}, errors.New("первоначальный взнос должен быть меньше стоимости")
	case rate < 0 || rate > 50:
		return Result{}, errors.New("ставка должна быть от 0 до 50%")
	case years < 1 || years > 35:
		return Result{}, errors.New("срок должен быть от 1 до 35 лет")
	}
	loan := price - down
	months := years * 12
	m := Monthly(loan, rate, months)
	total := m * float64(months)
	return Result{
		Price:        price,
		Down:         down,
		Loan:         loan,
		Rate:         rate,
		Years:        years,
		Monthly:      m,
		Total:        total,
		Overpay:      total - loan,
		IncomeNeeded: m / 0.5,
	}, nil
}
