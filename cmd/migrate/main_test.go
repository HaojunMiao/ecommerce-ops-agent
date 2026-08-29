package main

import "testing"

func TestValidateDownProtectsIrreversibleMigration(t *testing.T) {
	tests := []struct {
		name    string
		current uint
		steps   int
		wantErr bool
	}{
		{name: "cross floor", current: 31, steps: 1, wantErr: true},
		{name: "cross floor from later version", current: 34, steps: 4, wantErr: true},
		{name: "remain above floor", current: 34, steps: 3},
		{name: "older reversible migration", current: 30, steps: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateDown(tt.current, tt.steps); (got != nil) != tt.wantErr {
				t.Fatalf("validateDown(%d, %d) error = %v, wantErr %v", tt.current, tt.steps, got, tt.wantErr)
			}
		})
	}
}
