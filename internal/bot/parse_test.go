package bot

import "testing"

func TestParseMoney(t *testing.T) {
	cases := map[string]float64{
		"5 500 000":      5_500_000,
		"5500000":        5_500_000,
		"5,5 млн":        5_500_000,
		"5.5":            5_500_000,
		"5500 тыс":       5_500_000,
		"5500":           5_500_000,
		"до 4 млн":       4_000_000,
		"3 700 000 руб.": 3_700_000,
	}
	for in, want := range cases {
		if got, ok := ParseMoney(in); !ok || got != want {
			t.Errorf("ParseMoney(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "много", "0"} {
		if _, ok := ParseMoney(in); ok {
			t.Errorf("ParseMoney(%q) should fail", in)
		}
	}
}

func TestParseDown(t *testing.T) {
	price := 5_000_000.0
	cases := map[string]float64{
		"20%": 1_000_000, "20": 1_000_000, "1,2 млн": 1_200_000,
		"1 500 000": 1_500_000, "0": 0, "нет": 0,
	}
	for in, want := range cases {
		if got, ok := ParseDown(in, price); !ok || got != want {
			t.Errorf("ParseDown(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"100%", "6 млн", "abc"} {
		if _, ok := ParseDown(in, price); ok {
			t.Errorf("ParseDown(%q) should fail", in)
		}
	}
}

func TestParsePhone(t *testing.T) {
	ok := map[string]string{
		"+7 900 123-45-67":  "+79001234567",
		"89001234567":       "+79001234567",
		"9001234567":        "+79001234567",
		"8 (4725) 22-33-44": "+74725223344",
	}
	for in, want := range ok {
		if got, good := ParsePhone(in); !good || got != want {
			t.Errorf("ParsePhone(%q) = %q, %v", in, got, good)
		}
	}
	for _, in := range []string{"123", "+1 555 123 4567", "1001234567"} {
		if _, good := ParsePhone(in); good {
			t.Errorf("ParsePhone(%q) should fail", in)
		}
	}
}

func TestFormat(t *testing.T) {
	if got := FormatRub(5500000); got != "5 500 000 ₽" {
		t.Errorf("FormatRub = %q", got)
	}
	if got := FormatRub(999); got != "999 ₽" {
		t.Errorf("FormatRub = %q", got)
	}
	if got := FormatNumber(6.5); got != "6,5" {
		t.Errorf("FormatNumber = %q", got)
	}
	if got := FormatNumber(6); got != "6" {
		t.Errorf("FormatNumber = %q", got)
	}
	if got := yearsWord(21); got != "год" {
		t.Errorf("yearsWord(21) = %q", got)
	}
	if got := yearsWord(12); got != "лет" {
		t.Errorf("yearsWord(12) = %q", got)
	}
}
