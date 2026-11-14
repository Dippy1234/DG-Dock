package main

import (
	"strings"
	"testing"
)

func TestWidgetGapStyles(t *testing.T) {
	widget := &Item{Kind: KindWidget}
	for _, circle := range []bool{false, true} {
		d := &Dock{Scale: 1, WidgetGap: 5, CircleIcons: circle}
		if got := d.slotGap(widget); got != 5 {
			t.Errorf("CircleIcons=%v: widget gap = %.1f, want 5", circle, got)
		}
	}
	app := &Item{Kind: KindApp}
	d := &Dock{Scale: 1, WidgetGap: 16, G: 3, CircleIcons: true}
	if got := d.slotGap(app); got != 3 {
		t.Errorf("app slot gap = %.1f, want app gap 3", got)
	}
	// equal settings give equal gaps
	d = &Dock{}
	d.SetMetrics(1, 48, 0, 4, false, true, "pill")
	if d.slotGap(widget) != d.slotGap(app) {
		t.Errorf("widget gap %.1f != app gap %.1f by default", d.slotGap(widget), d.slotGap(app))
	}
}

func TestBackgroundCacheKeyHasNoFormattingErrors(t *testing.T) {
	key := (&Renderer{}).bgKeyFor(&Dock{W: 800, H: 120, Transparency: 25, Scale: 1})
	if strings.Contains(key, "%!") {
		t.Fatalf("background cache key contains formatting error: %s", key)
	}
}
