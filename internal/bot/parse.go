package bot

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	numRe        = regexp.MustCompile(`\d+(?:[.,]\d+)?`)
	digitSpaceRe = regexp.MustCompile(`(\d)[\s\x{00a0}]+(\d)`)
	nonDigitRe   = regexp.MustCompile(`\D`)
	negativeRe   = regexp.MustCompile(`(^|[^\d\s])\s*[-−]\s*\d`)
)

// joinDigitGroups склеивает «5 500 000» в «5500000».
func joinDigitGroups(s string) string {
	for {
		n := digitSpaceRe.ReplaceAllString(s, "$1$2")
		if n == s {
			return s
		}
		s = n
	}
}

// firstNumber находит первое число и текст после него.
func firstNumber(s string) (float64, string, bool) {
	loc := numRe.FindStringIndex(s)
	if loc == nil {
		return 0, "", false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(s[loc[0]:loc[1]], ",", "."), 64)
	if err != nil {
		return 0, "", false
	}
	return v, strings.TrimSpace(s[loc[1]:]), true
}

func unitMultiplier(rest string) float64 {
	switch {
	case strings.HasPrefix(rest, "млн"), strings.HasPrefix(rest, "миллион"),
		rest == "м", strings.HasPrefix(rest, "м "), strings.HasPrefix(rest, "м."):
		return 1e6
	case strings.HasPrefix(rest, "тыс"), rest == "т", rest == "к", rest == "k",
		strings.HasPrefix(rest, "т."), strings.HasPrefix(rest, "к "):
		return 1e3
	}
	return 0
}

// isNegative — во вводе есть отрицательное число («-5 млн», «до -3»). Диапазон «5-6» не считается.
func isNegative(s string) bool { return negativeRe.MatchString(strings.TrimSpace(s)) }

// FormatPhone: +79205952888 → +7 920 595-28-88.
func FormatPhone(p string) string {
	d := nonDigitRe.ReplaceAllString(p, "")
	if len(d) != 11 {
		return p
	}
	return "+7 " + d[1:4] + " " + d[4:7] + "-" + d[7:9] + "-" + d[9:11]
}

// ParseMoney понимает «5 500 000», «5,5 млн», «5500 тыс», «5.5».
// Без единиц: меньше 1000 — миллионы, меньше 100 000 — тысячи, иначе рубли.
func ParseMoney(s string) (float64, bool) {
	if isNegative(s) {
		return 0, false
	}
	t := joinDigitGroups(strings.ToLower(strings.TrimSpace(s)))
	v, rest, ok := firstNumber(t)
	if !ok || v <= 0 {
		return 0, false
	}
	if m := unitMultiplier(rest); m > 0 {
		v *= m
	} else if v < 1000 {
		v *= 1e6
	} else if v < 100000 {
		v *= 1e3
	}
	return math.Round(v), true
}

// ParseDown — первоначальный взнос: «20%», «20» (проценты), «1,5 млн», «0».
func ParseDown(s string, price float64) (float64, bool) {
	if isNegative(s) {
		return 0, false
	}
	t := joinDigitGroups(strings.ToLower(strings.TrimSpace(s)))
	switch t {
	case "0", "нет", "без взноса", "без первоначального взноса":
		return 0, true
	}
	v, rest, ok := firstNumber(t)
	if !ok || v < 0 {
		return 0, false
	}
	if strings.HasPrefix(rest, "%") || (unitMultiplier(rest) == 0 && v <= 100) {
		if v >= 100 {
			return 0, false
		}
		return math.Round(price * v / 100), true
	}
	m, ok := ParseMoney(t)
	if !ok || m >= price {
		return 0, false
	}
	return m, true
}

// ParseNumber — первое положительное число (площадь, ставка).
func ParseNumber(s string) (float64, bool) {
	if isNegative(s) {
		return 0, false
	}
	v, _, ok := firstNumber(joinDigitGroups(s))
	return v, ok && v > 0
}

// ParseYears — срок 1–35 лет из «20», «20 лет».
func ParseYears(s string) (int, bool) {
	if isNegative(s) {
		return 0, false
	}
	v, _, ok := firstNumber(s)
	if !ok || v != math.Trunc(v) || v < 1 || v > 35 {
		return 0, false
	}
	return int(v), true
}

// ParsePhone приводит российский номер к виду +7XXXXXXXXXX.
func ParsePhone(s string) (string, bool) {
	d := nonDigitRe.ReplaceAllString(s, "")
	switch {
	case len(d) == 11 && (d[0] == '7' || d[0] == '8'):
		d = d[1:]
	case len(d) == 10:
	default:
		return "", false
	}
	if d[0] != '9' && d[0] != '3' && d[0] != '4' && d[0] != '8' {
		return "", false
	}
	return "+7" + d, true
}

// FormatRub — «5 500 000 ₽».
func FormatRub(v float64) string {
	return groupThousands(int64(math.Round(v))) + " ₽"
}

func groupThousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// FormatNumber — число без лишних нулей: 6 → «6», 6.5 → «6,5».
func FormatNumber(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return strings.ReplaceAll(s, ".", ",")
}

// normalize — для сравнения ответа с кнопкой: без регистра, эмодзи и лишних пробелов.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r >= 'а' && r <= 'я', r == 'ё',
			r == ' ', r == '-', r == '–', r == '/', r == '%', r == ',', r == '.':
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
