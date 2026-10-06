package model

import "testing"

func TestRunHash(t *testing.T) {
	design := Design{Run: "run {foo} {bar}", Setup: "setup", Replicates: 1}
	point := Point{"foo": 1, "bar": map[string]any{"b": true, "a": "value"}}
	hash, err := design.RunHash(point, 1)
	if err != nil || len(hash) != 64 {
		t.Fatalf("hash = %q, %v", hash, err)
	}
	// Factor order and JSON-decoded numbers must not change the hash.
	reordered := Point{"bar": map[string]any{"a": "value", "b": true}, "foo": float64(1)}
	extended := design
	extended.Factors = Factors{"foo": {1, 2, 3}, "bar": {"value"}}
	extended.Replicates = 5
	if got, err := extended.RunHash(reordered, 1); err != nil || got != hash {
		t.Errorf("extended/reordered hash = %q, %v; want %q", got, err, hash)
	}
	for _, tc := range []struct {
		name      string
		design    Design
		point     Point
		replicate int
	}{
		{"run", Design{Run: "different", Setup: design.Setup}, point, 1},
		{"setup", Design{Run: design.Run, Setup: "different"}, point, 1},
		{"point", design, Point{"foo": "1", "bar": point["bar"]}, 1},
		{"replicate", design, point, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := tc.design.RunHash(tc.point, tc.replicate); err != nil || got == hash {
				t.Errorf("hash = %q, %v; want different from %q", got, err, hash)
			}
		})
	}
	if _, err := design.RunHash(Point{"foo": make(chan int)}, 1); err == nil {
		t.Error("unsupported setting was accepted")
	}
}
