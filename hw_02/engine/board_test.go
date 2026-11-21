package engine

import "testing"

// mk строит позицию 11x11: змейки заданы клетками (x,y) от головы к хвосту.
func mk(snakes [][][2]int, food [][2]int) (*Geom, *State) {
	g := NewGeom(11, 11)
	st := &State{N: int8(len(snakes))}
	for i, b := range snakes {
		cells := make([]int, len(b))
		for k, c := range b {
			cells[k] = c[1]*11 + c[0]
		}
		st.Snakes[i].SetBody(cells)
		st.Snakes[i].Health, st.Snakes[i].Alive = 90, true
	}
	for _, f := range food {
		st.Food.Set(f[1]*11 + f[0])
	}
	st.Rebuild()
	return g, st
}

func TestRules(t *testing.T) {
	// лобовое: длинная выживает
	g, st := mk([][][2]int{{{4, 5}, {3, 5}, {2, 5}, {1, 5}}, {{6, 5}, {7, 5}, {8, 5}}}, nil)
	mv := [MaxSnakes]int8{Right, Left}
	g.Step(st, &mv)
	if !st.Snakes[0].Alive || st.Snakes[1].Alive {
		t.Fatal("head-to-head longer should win")
	}
	// лобовое равных: обе гибнут
	g, st = mk([][][2]int{{{4, 5}, {3, 5}, {2, 5}}, {{6, 5}, {7, 5}, {8, 5}}}, nil)
	g.Step(st, &mv)
	if st.Snakes[0].Alive || st.Snakes[1].Alive {
		t.Fatal("equal head-to-head should kill both")
	}
	// ход в свой хвост, который уходит
	g, st = mk([][][2]int{{{5, 5}, {5, 6}, {6, 6}, {6, 5}}}, nil)
	mv = [MaxSnakes]int8{Right}
	g.Step(st, &mv)
	if !st.Snakes[0].Alive {
		t.Fatal("tail chase should survive")
	}
	// ход в сдвоенный хвост — гибель
	g, st = mk([][][2]int{{{5, 5}, {5, 6}, {6, 6}, {6, 5}, {6, 5}}}, nil)
	g.Step(st, &mv)
	if st.Snakes[0].Alive {
		t.Fatal("stacked tail should kill")
	}
	// еда: рост, здоровье, сдвоенный хвост
	g, st = mk([][][2]int{{{5, 5}, {4, 5}, {3, 5}}}, [][2]int{{6, 5}})
	g.Step(st, &mv)
	s := &st.Snakes[0]
	if !s.Alive || s.Len != 4 || s.Health != 100 || !st.Food.Zero() || !s.TailStacked() || s.Tail() != 5*11+4 {
		t.Fatalf("eat: len=%d hp=%d tail=%d", s.Len, s.Health, s.Tail())
	}
	// стена
	g, st = mk([][][2]int{{{10, 5}, {9, 5}, {8, 5}}}, nil)
	g.Step(st, &mv)
	if st.Snakes[0].Alive {
		t.Fatal("wall should kill")
	}
	// в шею соперника
	g, st = mk([][][2]int{{{4, 4}, {3, 4}, {2, 4}}, {{5, 5}, {5, 4}, {5, 3}}}, nil)
	mv = [MaxSnakes]int8{Right, Up}
	g.Step(st, &mv)
	if st.Snakes[0].Alive || !st.Snakes[1].Alive {
		t.Fatal("body collision")
	}
	// в хвост соперника, который уходит, — можно
	g, st = mk([][][2]int{{{4, 3}, {3, 3}, {2, 3}}, {{5, 5}, {5, 4}, {5, 3}}}, nil)
	g.Step(st, &mv)
	if !st.Snakes[0].Alive || !st.Snakes[1].Alive {
		t.Fatal("moving into other's tail should be fine")
	}
	if st.Occ.Count() != 6 {
		t.Fatalf("occ count %d", st.Occ.Count())
	}
}

func TestSearchAvoidsTrap(t *testing.T) {
	// змейка у стены: вниз — тупик из одной клетки, вверх — простор
	g, st := mk([][][2]int{{{0, 1}, {1, 1}, {1, 0}, {2, 0}}, {{9, 9}, {9, 8}, {9, 7}}}, nil)
	se := &Searcher{G: g, P: &DefaultParams, MaxNodes: 200000}
	r := se.Search(st, 20)
	if r.Move != Up {
		t.Fatalf("expected up, got %s score %d depth %d", MoveNames[r.Move], r.Score, r.Depth)
	}
}
