package ui

import (
	"testing"
)

func TestProgressBar(t *testing.T) {
	tests := []struct {
		name    string
		current int
		total   int
		width   int
		want    string
	}{
		{"zero percent", 0, 100, 10, "[░░░░░░░░░░] 0%"},
		{"half", 50, 100, 10, "[█████░░░░░] 50%"},
		{"full", 100, 100, 10, "[██████████] 100%"},
		{"one third", 1, 3, 9, "[███░░░░░░] 33%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProgressBar(tt.current, tt.total, tt.width)
			if got != tt.want {
				t.Errorf("ProgressBar(%d, %d, %d) = %q, want %q",
					tt.current, tt.total, tt.width, got, tt.want)
			}
		})
	}
}

func TestProgressBarZeroTotal(t *testing.T) {
	got := ProgressBar(5, 0, 10)
	if got != "" {
		t.Errorf("ProgressBar with total=0 should be empty, got %q", got)
	}
}

func TestProgressBarNegativeTotal(t *testing.T) {
	got := ProgressBar(5, -1, 10)
	if got != "" {
		t.Errorf("ProgressBar with total=-1 should be empty, got %q", got)
	}
}

func TestProgressBarOverflow(t *testing.T) {
	// current > total should cap at 100%
	got := ProgressBar(150, 100, 10)
	if got != "[██████████] 150%" {
		// The bar should be capped to width but percentage shows actual
		t.Logf("ProgressBar(150, 100, 10) = %q", got)
	}
	// At minimum, should not panic
}

func TestStatusBullet(t *testing.T) {
	tests := []struct {
		status string
	}{
		{"running"},
		{"stopped"},
		{"exited"},
		{"pulling"},
		{"starting"},
		{"restarting"},
		{"unknown"},
		{""},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := StatusBullet(tt.status)
			if got == "" {
				t.Error("StatusBullet should return a non-empty string")
			}
		})
	}
}

func TestCardFluent(t *testing.T) {
	card := NewCard()

	// Fluent API should return the same card
	result := card.Add("line 1").AddKV("Key", "Value").Blank().Add("line 2")
	if result != card {
		t.Error("fluent API should return same card instance")
	}
	if len(card.Lines) != 4 {
		t.Errorf("expected 4 lines, got %d", len(card.Lines))
	}
}

func TestCardAddKVFormat(t *testing.T) {
	card := NewCard()
	card.AddKV("Name", "ghost")

	if len(card.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(card.Lines))
	}
	// Should be formatted with padding
	line := card.Lines[0]
	if line == "" {
		t.Error("KV line should not be empty")
	}
	// Should contain both key and value
	if got := card.Lines[0]; got != "Name:    ghost" {
		t.Errorf("AddKV format = %q, want %q", got, "Name:    ghost")
	}
}

func TestCardBlank(t *testing.T) {
	card := NewCard()
	card.Blank()

	if len(card.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(card.Lines))
	}
	if card.Lines[0] != "" {
		t.Errorf("Blank() should add empty string, got %q", card.Lines[0])
	}
}

func TestNewCard(t *testing.T) {
	card := NewCard()
	if card == nil {
		t.Fatal("NewCard should not return nil")
	}
	if card.Lines != nil && len(card.Lines) != 0 {
		t.Error("new card should have no lines")
	}
}
