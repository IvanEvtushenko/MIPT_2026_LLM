package engine

import (
	"fmt"
	"math/rand"
)

// Rules — параметры режима для локальных игр (появление еды, сужение зоны).
type Rules struct {
	Name        string
	MinFood     int
	FoodChance  int
	ShrinkEvery int
	hz          [4]int // границы безопасной зоны: minX, maxX, minY, maxY
}

// NewGame создаёт стартовую позицию режима duel|standard|royale|constrictor на доске 11x11.
func NewGame(mode string, nSnakes int, rng *rand.Rand) (*Geom, *State, *Rules) {
	const W = 11
	g := NewGeom(W, W)
	r := &Rules{Name: "standard", MinFood: 1, FoodChance: 15}
	switch mode {
	case "royale":
		r.Name, r.ShrinkEvery = "royale", 25
		r.hz = [4]int{0, W - 1, 0, W - 1}
	case "constrictor":
		r.Name, r.MinFood, r.FoodChance = "constrictor", 0, 0
		g.Constrictor = true
	}
	st := &State{N: int8(nSnakes)}
	mn, md, mx := 1, (W-1)/2, W-2
	corners := [][2]int{{mn, mn}, {mn, mx}, {mx, mn}, {mx, mx}}
	cardinal := [][2]int{{mn, md}, {md, mn}, {md, mx}, {mx, md}}
	pts := corners
	if rng.Intn(2) == 0 {
		pts = cardinal
	}
	rng.Shuffle(len(pts), func(i, j int) { pts[i], pts[j] = pts[j], pts[i] })
	for i := 0; i < nSnakes; i++ {
		c := pts[i][1]*W + pts[i][0]
		s := &st.Snakes[i]
		s.SetBody([]int{c, c, c})
		s.Health, s.Alive = 100, true
	}
	st.Rebuild()
	if !g.Constrictor {
		cx, cy := md, md
		for i := 0; i < nSnakes; i++ {
			hx, hy := pts[i][0], pts[i][1]
			var cand []int
			for _, d := range [][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}} {
				px, py := hx+d[0], hy+d[1]
				if px == cx && py == cy {
					continue
				}
				c := py*W + px
				if st.Occ.Has(c) || st.Food.Has(c) {
					continue
				}
				away := (px < hx && hx < cx) || (cx < hx && hx < px) || (py < hy && hy < cy) || (cy < hy && hy < py)
				corner := (px == 0 || px == W-1) && (py == 0 || py == W-1)
				if away && !corner {
					cand = append(cand, c)
				}
			}
			if len(cand) > 0 {
				st.Food.Set(cand[rng.Intn(len(cand))])
			}
		}
		if c := cy*W + cx; !st.Occ.Has(c) {
			st.Food.Set(c)
		}
	}
	return g, st, r
}

// After — то, что движок делает после хода: новая еда и сужение зоны в royale.
func (r *Rules) After(g *Geom, st *State, rng *rand.Rand) {
	if r.ShrinkEvery > 0 && st.Turn > 0 && int(st.Turn)%r.ShrinkEvery == 0 && r.hz[0] <= r.hz[1] && r.hz[2] <= r.hz[3] {
		switch rng.Intn(4) {
		case 0:
			r.hz[0]++
		case 1:
			r.hz[1]--
		case 2:
			r.hz[2]++
		case 3:
			r.hz[3]--
		}
		var cells []int
		for y := 0; y < g.H; y++ {
			for x := 0; x < g.W; x++ {
				if x < r.hz[0] || x > r.hz[1] || y < r.hz[2] || y > r.hz[3] {
					cells = append(cells, y*g.W+x)
				}
			}
		}
		g.SetHazards(cells)
	}
	if r.MinFood == 0 && r.FoodChance == 0 {
		return
	}
	need := 0
	if cnt := st.Food.Count(); cnt < r.MinFood {
		need = r.MinFood - cnt
	} else if r.FoodChance > 0 && 100-rng.Intn(100) < r.FoodChance {
		need = 1
	}
	if need == 0 {
		return
	}
	free := g.Full.AndNot(st.Occ).AndNot(st.Food).AndNot(g.HazardBB)
	cells := make([]int, 0, 64)
	for m := free; !m.Zero(); {
		c := m.First()
		m.Clr(c)
		cells = append(cells, c)
	}
	for ; need > 0 && len(cells) > 0; need-- {
		k := rng.Intn(len(cells))
		st.Food.Set(cells[k])
		cells[k] = cells[len(cells)-1]
		cells = cells[:len(cells)-1]
	}
}

// BotMove — простые соперники для проверки: random и greedy (к ближайшей еде, не в тупик).
func BotMove(kind string, g *Geom, st *State, i int, rng *rand.Rand) int {
	mv, n := g.SafeMoves(st, i)
	if kind == "random" || n == 1 {
		return int(mv[rng.Intn(n)])
	}
	s := &st.Snakes[i]
	free := g.Full.AndNot(st.Occ).AndNot(g.Static)
	danger := BB{}
	for j := 0; j < int(st.N); j++ {
		o := &st.Snakes[j]
		if j != i && o.Alive && o.Len >= s.Len {
			var m BB
			m.Set(o.Head())
			danger = danger.Or(g.Neighbors(m))
		}
	}
	best, bestS := int(mv[0]), -1<<30
	for k := 0; k < n; k++ {
		c := g.Nbr(s.Head(), int(mv[k]))
		var reach BB
		reach.Set(c)
		fd := 99
		for d := 0; d < 40; d++ {
			if fd == 99 && !reach.And(st.Food).Zero() {
				fd = d
			}
			nx := g.Neighbors(reach).And(free).Or(reach)
			if nx == reach {
				break
			}
			reach = nx
		}
		sc := -fd * 3
		if reach.Count() < int(s.Len) {
			sc -= 1000
		}
		if danger.Has(c) {
			sc -= 500
		}
		if sc > bestS {
			best, bestS = int(mv[k]), sc
		}
	}
	return best
}

// ToRequest собирает запрос API для змейки i (для внешних змеек по HTTP).
func ToRequest(g *Geom, st *State, i int, rules *Rules, gameID string, timeout int) *Request {
	coord := func(c int) Coord { return Coord{c % g.W, c / g.W} }
	snake := func(j int) SnakeJSON {
		s := &st.Snakes[j]
		body := make([]Coord, s.Len)
		for k := range body {
			body[k] = coord(s.Seg(k))
		}
		return SnakeJSON{ID: fmt.Sprintf("s%d", j), Name: fmt.Sprintf("s%d", j), Health: int(s.Health),
			Body: body, Head: body[0], Length: int(s.Len), Latency: "0"}
	}
	r := &Request{Turn: int(st.Turn)}
	r.Game.ID, r.Game.Timeout, r.Game.Source = gameID, timeout, "custom"
	r.Game.Ruleset.Name = rules.Name
	r.Game.Ruleset.Settings.FoodSpawnChance = rules.FoodChance
	r.Game.Ruleset.Settings.MinimumFood = rules.MinFood
	r.Game.Ruleset.Settings.HazardDamagePerTurn = 14
	r.Game.Ruleset.Settings.Royale.ShrinkEveryNTurns = rules.ShrinkEvery
	r.Game.Map = "standard"
	if rules.Name == "royale" {
		r.Game.Map = "royale"
	}
	r.Board.Width, r.Board.Height = g.W, g.H
	r.Board.Food, r.Board.Hazards, r.Board.Snakes = []Coord{}, []Coord{}, []SnakeJSON{}
	for m := st.Food; !m.Zero(); {
		c := m.First()
		m.Clr(c)
		r.Board.Food = append(r.Board.Food, coord(c))
	}
	for c := 0; c < g.N; c++ {
		for k := 0; k < int(g.Hazard[c]); k++ {
			r.Board.Hazards = append(r.Board.Hazards, coord(c))
		}
	}
	for j := 0; j < int(st.N); j++ {
		if st.Snakes[j].Alive {
			r.Board.Snakes = append(r.Board.Snakes, snake(j))
		}
	}
	r.You = snake(i)
	return r
}
