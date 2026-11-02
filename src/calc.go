package main

// Quick sums in search: + - * / % ^, brackets, "x" or "×" for times, "," decimals ok.

import (
	"math"
	"strconv"
	"strings"
)

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
	v := p.power()
	for {
		switch p.peek() {
		case '*':
			p.pos++
			v *= p.power()
		case '/':
			p.pos++
			d := p.power()
			if d == 0 {
				p.err = true
				return 0
			}
			v /= d
		case '%':
			p.pos++
			d := p.power()
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

func (p *calcParser) power() float64 {
	v := p.unary()
	if p.peek() == '^' {
		p.pos++
		v = math.Pow(v, p.power())
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
	return p.atom()
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
	q = strings.NewReplacer("×", "*", "x", "*", "X", "*", "÷", "/", ",", ".").Replace(q)
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
