// Package engine: правила Battlesnake, поиск хода и оценка позиции.
package engine

import "math/bits"

// BB — битовая маска клеток доски (до 128 клеток, индекс y*W+x).
type BB struct{ Lo, Hi uint64 }

func (b BB) Has(i int) bool {
	if i < 64 {
		return b.Lo>>uint(i)&1 == 1
	}
	return b.Hi>>uint(i-64)&1 == 1
}

func (b *BB) Set(i int) {
	if i < 64 {
		b.Lo |= 1 << uint(i)
	} else {
		b.Hi |= 1 << uint(i-64)
	}
}

func (b *BB) Clr(i int) {
	if i < 64 {
		b.Lo &^= 1 << uint(i)
	} else {
		b.Hi &^= 1 << uint(i-64)
	}
}

func (a BB) And(b BB) BB    { return BB{a.Lo & b.Lo, a.Hi & b.Hi} }
func (a BB) Or(b BB) BB     { return BB{a.Lo | b.Lo, a.Hi | b.Hi} }
func (a BB) AndNot(b BB) BB { return BB{a.Lo &^ b.Lo, a.Hi &^ b.Hi} }
func (a BB) Zero() bool     { return a.Lo|a.Hi == 0 }
func (a BB) Count() int     { return bits.OnesCount64(a.Lo) + bits.OnesCount64(a.Hi) }
func (a BB) shl(n uint) BB  { return BB{a.Lo << n, a.Hi<<n | a.Lo>>(64-n)} }
func (a BB) shr(n uint) BB  { return BB{a.Lo>>n | a.Hi<<(64-n), a.Hi >> n} }

// First возвращает индекс младшей единицы (маска не пустая).
func (a BB) First() int {
	if a.Lo != 0 {
		return bits.TrailingZeros64(a.Lo)
	}
	return 64 + bits.TrailingZeros64(a.Hi)
}

const (
	MaxCells  = 128
	MaxSnakes = 4
	Ring      = 128
	Up        = 0
	Down      = 1
	Left      = 2
	Right     = 3
)

var MoveNames = [4]string{"up", "down", "left", "right"}

// Geom — неизменные в пределах хода свойства доски и правил.
type Geom struct {
	W, H, N                 int
	Full, NotLeft, NotRight BB
	Hazard                  [MaxCells]uint8 // сколько слоёв опасной зоны в клетке
	HazardBB                BB
	HazardDamage            int
	Constrictor             bool
	Static                  BB // постоянные препятствия (змейки сверх MaxSnakes)
	nbr                     [MaxCells][4]int16
}

func NewGeom(w, h int) *Geom {
	g := &Geom{W: w, H: h, N: w * h, HazardDamage: 14}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			g.Full.Set(i)
			if x != 0 {
				g.NotLeft.Set(i)
			}
			if x != w-1 {
				g.NotRight.Set(i)
			}
			g.nbr[i] = [4]int16{-1, -1, -1, -1}
			if y+1 < h {
				g.nbr[i][Up] = int16(i + w)
			}
			if y > 0 {
				g.nbr[i][Down] = int16(i - w)
			}
			if x > 0 {
				g.nbr[i][Left] = int16(i - 1)
			}
			if x+1 < w {
				g.nbr[i][Right] = int16(i + 1)
			}
		}
	}
	return g
}

func (g *Geom) Nbr(c, d int) int { return int(g.nbr[c][d]) }

// Neighbors — все клетки, соседние с маской.
func (g *Geom) Neighbors(m BB) BB {
	w := uint(g.W)
	return m.shl(1).And(g.NotLeft).Or(m.shr(1).And(g.NotRight)).Or(m.shl(w)).Or(m.shr(w)).And(g.Full)
}

func (g *Geom) SetHazards(cells []int) {
	g.Hazard = [MaxCells]uint8{}
	g.HazardBB = BB{}
	for _, c := range cells {
		g.Hazard[c]++
		g.HazardBB.Set(c)
	}
}

func (g *Geom) Dist(a, b int) int {
	ax, ay, bx, by := a%g.W, a/g.W, b%g.W, b/g.W
	return abs(ax-bx) + abs(ay-by)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Snake хранит тело в кольцевом буфере: голова — Body[HeadI], k-й сегмент — Body[HeadI-k].
type Snake struct {
	Body   [Ring]uint8
	HeadI  uint8
	Len    uint8
	Health int16
	Alive  bool
}

func (s *Snake) Seg(k int) int { return int(s.Body[(int(s.HeadI)-k)&(Ring-1)]) }
func (s *Snake) Head() int     { return int(s.Body[s.HeadI&(Ring-1)]) }
func (s *Snake) Tail() int     { return s.Seg(int(s.Len) - 1) }

// TailStacked: хвост сдвоен (змейка ела на прошлом ходу), на следующем ходу он не освободится.
func (s *Snake) TailStacked() bool {
	return s.Len >= 2 && s.Seg(int(s.Len)-1) == s.Seg(int(s.Len)-2)
}

func (s *Snake) grow() {
	t := int(s.HeadI) - int(s.Len) + 1
	s.Body[(t-1)&(Ring-1)] = s.Body[t&(Ring-1)]
	s.Len++
}

// SetBody задаёт тело списком клеток от головы к хвосту.
func (s *Snake) SetBody(cells []int) {
	n := len(cells)
	s.Len = uint8(n)
	s.HeadI = uint8(n - 1)
	for k, c := range cells {
		s.Body[(n-1-k)&(Ring-1)] = uint8(c)
	}
}

// State — копируемая позиция (без выделений памяти).
type State struct {
	Snakes [MaxSnakes]Snake
	Food   BB
	Occ    BB // все сегменты живых змеек
	N      int8
	Turn   int16
}

func (st *State) Rebuild() {
	st.Occ = BB{}
	for i := 0; i < int(st.N); i++ {
		s := &st.Snakes[i]
		if !s.Alive {
			continue
		}
		for k := 0; k < int(s.Len); k++ {
			st.Occ.Set(s.Seg(k))
		}
	}
}

func (st *State) bodyNoHeads() BB {
	var b BB
	for i := 0; i < int(st.N); i++ {
		s := &st.Snakes[i]
		if !s.Alive {
			continue
		}
		for k := 1; k < int(s.Len); k++ {
			b.Set(s.Seg(k))
		}
	}
	return b
}

func (st *State) AliveCount() int {
	n := 0
	for i := 0; i < int(st.N); i++ {
		if st.Snakes[i].Alive {
			n++
		}
	}
	return n
}

// Swap меняет местами змеек i и j (поиск считает «своей» змейку 0).
func (st *State) Swap(i, j int) { st.Snakes[i], st.Snakes[j] = st.Snakes[j], st.Snakes[i] }

// Step применяет один ход всех змеек по официальным правилам:
// движение, голод, опасная зона, еда, выбывание. Новая еда здесь не появляется.
func (g *Geom) Step(st *State, moves *[MaxSnakes]int8) {
	n := int(st.N)
	var newHead [MaxSnakes]int
	body := st.Occ.Or(g.Static)
	firstPass := false
	for i := 0; i < n; i++ {
		s := &st.Snakes[i]
		if !s.Alive {
			continue
		}
		newHead[i] = int(g.nbr[s.Head()][moves[i]&3])
		if !s.TailStacked() {
			body.Clr(s.Tail())
		}
	}
	var eaten BB
	for i := 0; i < n; i++ {
		s := &st.Snakes[i]
		if !s.Alive {
			continue
		}
		nh := newHead[i]
		if nh < 0 {
			s.Alive = false
			firstPass = true
			continue
		}
		s.HeadI++
		s.Body[s.HeadI&(Ring-1)] = uint8(nh)
		if g.Constrictor {
			s.Health = 100
			s.grow()
			continue
		}
		s.Health--
		onFood := st.Food.Has(nh)
		if g.Hazard[nh] > 0 && !onFood {
			s.Health -= int16(g.HazardDamage * int(g.Hazard[nh]))
			if s.Health < 0 {
				s.Health = 0
			}
		}
		if onFood {
			s.Health = 100
			s.grow()
			eaten.Set(nh)
		}
		if s.Health <= 0 {
			s.Alive = false
			firstPass = true
		}
	}
	st.Food = st.Food.AndNot(eaten)
	if firstPass {
		body = st.bodyNoHeads().Or(g.Static)
	}
	var dead [MaxSnakes]bool
	anyDead := firstPass
	for i := 0; i < n; i++ {
		s := &st.Snakes[i]
		if !s.Alive {
			continue
		}
		h := s.Head()
		if body.Has(h) {
			dead[i] = true
			anyDead = true
			continue
		}
		for j := 0; j < n; j++ {
			o := &st.Snakes[j]
			if j != i && o.Alive && o.Head() == h && s.Len <= o.Len {
				dead[i] = true
				anyDead = true
				break
			}
		}
	}
	if anyDead {
		for i := 0; i < n; i++ {
			if dead[i] {
				st.Snakes[i].Alive = false
			}
		}
		st.Rebuild()
	} else {
		st.Occ = body.AndNot(g.Static)
		for i := 0; i < n; i++ {
			if st.Snakes[i].Alive {
				st.Occ.Set(st.Snakes[i].Head())
			}
		}
	}
	st.Turn++
}

// SafeMoves — ходы, которые не ведут в стену или в тело, которое точно останется на месте.
// Если таких нет, возвращает один ход (змейка всё равно погибнет).
func (g *Geom) SafeMoves(st *State, i int) (mv [4]int8, n int) {
	blocked := st.Occ.Or(g.Static)
	for j := 0; j < int(st.N); j++ {
		o := &st.Snakes[j]
		if o.Alive && !o.TailStacked() {
			blocked.Clr(o.Tail())
		}
	}
	h := st.Snakes[i].Head()
	for d := 0; d < 4; d++ {
		c := g.nbr[h][d]
		if c >= 0 && !blocked.Has(int(c)) {
			mv[n] = int8(d)
			n++
		}
	}
	if n == 0 {
		for d := 0; d < 4; d++ {
			if g.nbr[h][d] >= 0 {
				mv[0] = int8(d)
				return mv, 1
			}
		}
		mv[0], n = Up, 1
	}
	return mv, n
}
