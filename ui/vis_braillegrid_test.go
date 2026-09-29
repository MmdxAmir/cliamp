package ui

import "testing"

// The LCG constants are part of every seeded animation, so the stream must
// stay the same bit for bit.
func TestLCGNext(t *testing.T) {
	tests := []struct {
		name string
		seed uint64
		want []uint64
	}{
		{name: "zero seed", seed: 0, want: []uint64{167951807, 218396424, 1299921937}},
		{name: "sand seed", seed: 0x5A4D5A4D5A4D, want: []uint64{895402528, 2039119844, 1944890791}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.seed
			for i, want := range tt.want {
				if got := lcgNext(&state); got != want {
					t.Fatalf("draw %d = %d, want %d", i, got, want)
				}
			}
		})
	}
}

func TestRng64UsesLCGNext(t *testing.T) {
	a, b := uint64(0xFEED5EED), uint64(0xFEED5EED)
	for i := range 8 {
		want := float64(lcgNext(&b)%1000) / 1000.0
		if got := rng64(&a); got != want {
			t.Fatalf("draw %d = %v, want %v", i, got, want)
		}
	}
}
