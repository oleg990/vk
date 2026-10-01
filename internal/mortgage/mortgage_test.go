package mortgage

import (
	"math"
	"testing"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestMonthly(t *testing.T) {
	// 1 000 000 ₽ под 12% на 12 месяцев — классический пример: 88 848,79 ₽.
	if got := Monthly(1_000_000, 12, 12); !near(got, 88848.79, 0.01) {
		t.Fatalf("Monthly = %.2f, want 88848.79", got)
	}
	if got := Monthly(1_200_000, 0, 12); got != 100_000 {
		t.Fatalf("zero rate = %.2f, want 100000", got)
	}
	if got := Monthly(0, 10, 12); got != 0 {
		t.Fatalf("zero loan = %.2f", got)
	}
}

func TestCalculate(t *testing.T) {
	r, err := Calculate(6_000_000, 1_200_000, 12, 20)
	if err != nil {
		t.Fatal(err)
	}
	if r.Loan != 4_800_000 {
		t.Fatalf("loan = %v", r.Loan)
	}
	if !near(r.Total, r.Monthly*240, 0.001) || !near(r.Overpay, r.Total-r.Loan, 0.001) {
		t.Fatalf("totals inconsistent: %+v", r)
	}
	if !near(r.IncomeNeeded, r.Monthly*2, 0.001) {
		t.Fatalf("income = %v", r.IncomeNeeded)
	}
}

func TestCalculateErrors(t *testing.T) {
	cases := []struct {
		price, down, rate float64
		years             int
	}{
		{0, 0, 10, 10},
		{5e6, 5e6, 10, 10},
		{5e6, -1, 10, 10},
		{5e6, 1e6, 60, 10},
		{5e6, 1e6, 10, 0},
		{5e6, 1e6, 10, 40},
	}
	for _, c := range cases {
		if _, err := Calculate(c.price, c.down, c.rate, c.years); err == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
}
