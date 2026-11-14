package main

// Quick sums in search: + - * / % ^, brackets, "x" or "×" for times. A comma before
// exactly three digits is a thousands separator (1,000), any other comma is a decimal
// point (1,5). Dates and phone numbers are left alone rather than worked out as sums.

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	reThousands = regexp.MustCompile(`(\d),(\d{3})(?:[^\d]|$)`)
	reDateLike  = regexp.MustCompile(`^(?:\d{4}[-/.]\d{1,2}[-/.]\d{1,2}|\d{1,2}[-/.]\d{1,2}[-/.]\d{2,4})$`)
	rePhoneLike = regexp.MustCompile(`^\+?\d[\d -]*$`)
)

// notASum reports queries that only look like sums: dates (2026-11-07, 07/11/2026)
// and phone numbers (020-7946-0958, +44 20 7946 0958).
func notASum(q string) bool {
	q = strings.TrimSpace(q)
	if reDateLike.MatchString(q) {
		return true
	}
	if rePhoneLike.MatchString(q) && !strings.Contains(q, " - ") {
		digits := 0
		for _, r := range q {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		groups := strings.FieldsFunc(q, func(r rune) bool { return r == '-' || r == ' ' || r == '+' })
		if strings.HasPrefix(q, "+") || (digits >= 9 && len(groups) >= 2) ||
			(len(groups) >= 3 && strings.HasPrefix(groups[0], "0")) {
			return true
		}
	}
	return false
}

// normaliseCommas removes thousands separators and turns decimal commas into points.
func normaliseCommas(q string) string {
	for {
		n := reThousands.ReplaceAllStringFunc(q, func(m string) string {
			return strings.Replace(m, ",", "", 1)
		})
		if n == q {
			break
		}
		q = n
	}
	return strings.ReplaceAll(q, ",", ".")
}

type calcParser struct {
	s   []rune
	pos int
	err bool
}

func (p *calcParser) peek() rune {
	for p.pos < len(p.s) && p.s[p.pos] == ' ' {
		p.pos++
	}
	if p.pos >= len(p.s) {
		return 0
	}
	return p.s[p.pos]
}

func (p *calcParser) expr() float64 {
	v := p.term()
	for {
		switch p.peek() {
		case '+':
			p.pos++
			v += p.term()
		case '-':
			p.pos++
			v -= p.term()
		default:
			return v
		}
	}
}

func (p *calcParser) term() float64 {
	v := p.unary()
	for {
		switch p.peek() {
		case '*':
			p.pos++
			v *= p.unary()
		case '/':
			p.pos++
			d := p.unary()
			if d == 0 {
				p.err = true
				return 0
			}
			v /= d
		case '%':
			p.pos++
			d := p.unary()
			if d == 0 {
				p.err = true
				return 0
			}
			v = math.Mod(v, d)
		default:
			return v
		}
	}
}

// power binds tighter than a leading minus, as in maths: -2^2 = -(2^2) = -4.
// It is right-associative (2^3^2 = 2^9) and allows a signed exponent (2^-1).
func (p *calcParser) power() float64 {
	v := p.atom()
	if p.peek() == '^' {
		p.pos++
		v = math.Pow(v, p.unary())
	}
	return v
}

func (p *calcParser) unary() float64 {
	switch p.peek() {
	case '-':
		p.pos++
		return -p.unary()
	case '+':
		p.pos++
		return p.unary()
	}
	return p.power()
}

func (p *calcParser) atom() float64 {
	if p.peek() == '(' {
		p.pos++
		v := p.expr()
		if p.peek() != ')' {
			p.err = true
			return 0
		}
		p.pos++
		return v
	}
	start := p.pos
	for p.pos < len(p.s) && (p.s[p.pos] >= '0' && p.s[p.pos] <= '9' || p.s[p.pos] == '.') {
		p.pos++
	}
	if start == p.pos {
		p.err = true
		return 0
	}
	v, err := strconv.ParseFloat(string(p.s[start:p.pos]), 64)
	if err != nil {
		p.err = true
	}
	return v
}

// evalCalc returns the result of q if it's a sum (needs at least one operator).
func evalCalc(q string) (string, bool) {
	q = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q), "="))
	if notASum(q) {
		return "", false
	}
	q = normaliseCommas(strings.NewReplacer("×", "*", "x", "*", "X", "*", "÷", "/").Replace(q))
	if q == "" || !strings.ContainsAny(strings.TrimLeft(q, "+- "), "+-*/%^") || !strings.ContainsAny(q, "0123456789") {
		return "", false
	}
	for _, r := range q {
		if !strings.ContainsRune("0123456789.+-*/%^() ", r) {
			return "", false
		}
	}
	p := &calcParser{s: []rune(q)}
	v := p.expr()
	if p.err || p.peek() != 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "", false
	}
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if math.Abs(v) >= 1e15 || (math.Abs(v) < 1e-6 && v != 0) {
		s = strconv.FormatFloat(v, 'g', 10, 64)
	} else if strings.Contains(s, ".") && len(s) > 14 {
		s = strconv.FormatFloat(v, 'f', 10, 64)
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s, true
}
