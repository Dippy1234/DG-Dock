package main

import (
	"testing"
	"time"
)

func TestBudTimeline(t *testing.T) {
	d := &Dock{}
	sep := &Item{Kind: KindSep, InRun: true}
	var run []*Item
	d.Items = append(d.Items, &Item{Kind: KindApp, Appear: 1}, sep)
	for i := 0; i < 4; i++ {
		it := &Item{Kind: KindRunning, InRun: true}
		run = append(run, it)
		d.Items = append(d.Items, it)
	}
	t0 := time.Now()
	d.BudStart, d.BudIn = t0, true
	at := func(ms int) {
		d.stepBud(t0.Add(time.Duration(ms) * time.Millisecond))
	}
	at(150)
	if sep.Appear < 0.4 || sep.Appear > 0.6 || run[0].Appear != sep.Appear || run[1].Appear != 0 {
		t.Fatalf("mid-split: sep %.2f first %.2f second %.2f", sep.Appear, run[0].Appear, run[1].Appear)
	}
	at(330)
	if run[1].Appear <= 0 || run[2].Appear >= run[1].Appear {
		t.Fatalf("staggered: %.2f %.2f %.2f", run[1].Appear, run[2].Appear, run[3].Appear)
	}
	at(3000)
	if !d.BudStart.IsZero() || run[3].Appear != 1 {
		t.Fatalf("should be done: %v %.2f", d.BudStart, run[3].Appear)
	}
}
