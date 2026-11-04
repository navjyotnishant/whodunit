package attribution

import "testing"

func TestIntersectCountsDistinctMatches(t *testing.T) {
	agent := map[uint64]struct{}{1: {}, 2: {}, 9: {}}
	cases := []struct {
		staged []uint64
		want   int
	}{
		{nil, 0},
		{[]uint64{3, 4}, 0},
		{[]uint64{1, 3}, 1},
		{[]uint64{1, 1, 1, 2}, 2}, // a repeated line claims once
		{[]uint64{9, 2, 1}, 3},
	}
	for _, c := range cases {
		if got := Intersect(c.staged, agent); got != c.want {
			t.Errorf("Intersect(%v) = %d, want %d", c.staged, got, c.want)
		}
	}
}
