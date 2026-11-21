package engine

import (
	"sort"
	"strings"
)

// Типы запроса Battlesnake API v1.
type Coord struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type SnakeJSON struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Health  int     `json:"health"`
	Body    []Coord `json:"body"`
	Head    Coord   `json:"head"`
	Length  int     `json:"length"`
	Latency any     `json:"latency"`
	Shout   string  `json:"shout"`
}

type Ruleset struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Settings struct {
		FoodSpawnChance     int `json:"foodSpawnChance"`
		MinimumFood         int `json:"minimumFood"`
		HazardDamagePerTurn int `json:"hazardDamagePerTurn"`
		Royale              struct {
			ShrinkEveryNTurns int `json:"shrinkEveryNTurns"`
		} `json:"royale"`
	} `json:"settings"`
}

type GameJSON struct {
	ID      string  `json:"id"`
	Ruleset Ruleset `json:"ruleset"`
	Map     string  `json:"map"`
	Timeout int     `json:"timeout"`
	Source  string  `json:"source"`
}

type BoardJSON struct {
	Height  int         `json:"height"`
	Width   int         `json:"width"`
	Food    []Coord     `json:"food"`
	Hazards []Coord     `json:"hazards"`
	Snakes  []SnakeJSON `json:"snakes"`
}

type Request struct {
	Game  GameJSON  `json:"game"`
	Turn  int       `json:"turn"`
	Board BoardJSON `json:"board"`
	You   SnakeJSON `json:"you"`
}

// Build переводит запрос в позицию; «наша» змейка — с индексом 0.
// ok=false, если доска больше поддерживаемой (тогда используется Fallback).
func Build(r *Request) (*Geom, *State, bool) {
	w, h := r.Board.Width, r.Board.Height
	if w*h > MaxCells || w < 2 || h < 2 || len(r.You.Body) == 0 {
		return nil, nil, false
	}
	g := NewGeom(w, h)
	if strings.Contains(r.Game.Ruleset.Name, "wrapped") {
		return nil, nil, false
	}
	g.Constrictor = strings.Contains(r.Game.Ruleset.Name, "constrictor")
	g.HazardDamage = r.Game.Ruleset.Settings.HazardDamagePerTurn
	if g.HazardDamage == 0 && len(r.Board.Hazards) > 0 {
		g.HazardDamage = 14
	}
	cell := func(c Coord) int { return c.Y*w + c.X }
	hz := make([]int, 0, len(r.Board.Hazards))
	for _, c := range r.Board.Hazards {
		if c.X >= 0 && c.X < w && c.Y >= 0 && c.Y < h {
			hz = append(hz, cell(c))
		}
	}
	g.SetHazards(hz)
	st := &State{Turn: int16(r.Turn)}
	for _, f := range r.Board.Food {
		st.Food.Set(cell(f))
	}
	others := make([]SnakeJSON, 0, len(r.Board.Snakes))
	for _, s := range r.Board.Snakes {
		if s.ID != r.You.ID && len(s.Body) > 0 {
			others = append(others, s)
		}
	}
	myHead := cell(r.You.Body[0])
	sort.SliceStable(others, func(a, b int) bool {
		return g.Dist(myHead, cell(others[a].Body[0])) < g.Dist(myHead, cell(others[b].Body[0]))
	})
	put := func(i int, s SnakeJSON) bool {
		if len(s.Body) >= Ring {
			return false
		}
		cells := make([]int, len(s.Body))
		for k, c := range s.Body {
			cells[k] = cell(c)
		}
		sn := &st.Snakes[i]
		sn.SetBody(cells)
		sn.Health = int16(s.Health)
		sn.Alive = true
		return true
	}
	if !put(0, r.You) {
		return nil, nil, false
	}
	st.N = 1
	for _, s := range others {
		if int(st.N) < MaxSnakes {
			if !put(int(st.N), s) {
				return nil, nil, false
			}
			st.N++
		} else {
			for _, c := range s.Body {
				g.Static.Set(cell(c))
			}
		}
	}
	st.Rebuild()
	return g, st, true
}

// Fallback — простой ход для неподдерживаемых досок: безопасная клетка с наибольшей заливкой.
func Fallback(r *Request) string {
	w, h := r.Board.Width, r.Board.Height
	if w <= 0 || h <= 0 || len(r.You.Body) == 0 {
		return "up"
	}
	occ := make([]bool, w*h)
	for _, s := range r.Board.Snakes {
		for k, c := range s.Body {
			if k == len(s.Body)-1 && len(s.Body) > 1 {
				continue
			}
			if c.X >= 0 && c.X < w && c.Y >= 0 && c.Y < h {
				occ[c.Y*w+c.X] = true
			}
		}
	}
	dirs := []struct {
		dx, dy int
		name   string
	}{{0, 1, "up"}, {0, -1, "down"}, {-1, 0, "left"}, {1, 0, "right"}}
	hd := r.You.Body[0]
	best, bestN := "up", -1
	for _, d := range dirs {
		x, y := hd.X+d.dx, hd.Y+d.dy
		if strings.Contains(r.Game.Ruleset.Name, "wrapped") {
			x, y = (x+w)%w, (y+h)%h
		}
		if x < 0 || x >= w || y < 0 || y >= h || occ[y*w+x] {
			continue
		}
		seen := make([]bool, w*h)
		q := []int{y*w + x}
		seen[y*w+x] = true
		for len(q) > 0 && len(q) < 400 {
			c := q[0]
			q = q[1:]
			cx, cy := c%w, c/w
			for _, e := range dirs {
				nx, ny := cx+e.dx, cy+e.dy
				if nx < 0 || nx >= w || ny < 0 || ny >= h {
					continue
				}
				ni := ny*w + nx
				if !seen[ni] && !occ[ni] {
					seen[ni] = true
					q = append(q, ni)
				}
			}
		}
		cnt := 0
		for _, v := range seen {
			if v {
				cnt++
			}
		}
		if cnt > bestN {
			best, bestN = d.name, cnt
		}
	}
	return best
}
