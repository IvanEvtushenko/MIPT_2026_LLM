package engine

import (
	"time"
)

// Params — веса оценки позиции; их подбирает локальный hill-climbing.
type Params struct {
	Terr      int // за клетку территории (кто раньше дойдёт)
	Len       int // за единицу разницы длин в пределах LenCap
	LenCap    int
	LenExtra  int // за единицу разницы длин сверх LenCap
	LenWant   int // хотим быть длиннее соперника хотя бы на столько
	Hungry    int // порог здоровья, ниже которого ищем еду
	FoodDist  int // штраф за шаг до ближайшей своей еды, когда голодны или короче
	Starve    int // штраф, если до еды не дотянуть по здоровью
	Trap      int // за клетку нехватки места относительно длины
	Health    int // за единицу здоровья (важно в royale)
	HazardPen int // штраф за свою клетку территории в опасной зоне, в десятых долях клетки
	Kill      int // за каждого выбывшего соперника (режимы на 4)
	MaxAdv    int // сколько ближайших соперников перебирать честно (режимы на 4)
	DrawOK    int // соперник идёт на взаимную гибель, только если без неё наш перевес больше этого
}

var DefaultParams = Params{
	Terr: 10, Len: 40, LenCap: 4, LenExtra: 5, LenWant: 2, Hungry: 35, FoodDist: 6,
	Starve: 3000, Trap: 60, Health: 1, HazardPen: 5, Kill: 600, MaxAdv: 2, DrawOK: 1500,
}

const (
	WinScore  = 1_000_000
	LossScore = -1_000_000
	DrawScore = -100
	maxFree   = 32
)

// Searcher ищет ход змейки 0 перебором «мы ходим, соперники отвечают худшим для нас образом».
type Searcher struct {
	G        *Geom
	P        *Params
	Deadline time.Time
	MaxNodes int64
	Nodes    int64
	stop     bool
	adv      []int
	others   []int
}

type Result struct {
	Move  int
	Score int
	Depth int
	Nodes int64
}

func (se *Searcher) timeUp() bool {
	if se.stop {
		return true
	}
	if se.Nodes&1023 == 0 {
		if se.MaxNodes > 0 {
			se.stop = se.Nodes >= se.MaxNodes
		} else {
			se.stop = time.Now().After(se.Deadline)
		}
	}
	return se.stop
}

// Search: итеративное углубление до дедлайна или лимита узлов.
func (se *Searcher) Search(st *State, maxDepth int) Result {
	g := se.G
	me := &st.Snakes[0]
	mv, n := g.SafeMoves(st, 0)
	res := Result{Move: int(mv[0]), Score: LossScore}
	if !me.Alive {
		return res
	}
	// соперники: ближайшие MaxAdv перебираем, остальные ходят простым правилом
	se.adv, se.others = se.adv[:0], se.others[:0]
	type od struct{ i, d int }
	var ods []od
	for j := 1; j < int(st.N); j++ {
		if st.Snakes[j].Alive {
			ods = append(ods, od{j, g.Dist(me.Head(), st.Snakes[j].Head())})
		}
	}
	for a := 1; a < len(ods); a++ {
		for b := a; b > 0 && ods[b].d < ods[b-1].d; b-- {
			ods[b], ods[b-1] = ods[b-1], ods[b]
		}
	}
	maxAdv := se.P.MaxAdv
	if maxAdv < 1 {
		maxAdv = 1
	}
	for k, o := range ods {
		if k < maxAdv {
			se.adv = append(se.adv, o.i)
		} else {
			se.others = append(se.others, o.i)
		}
	}
	if n == 1 {
		res.Depth = 0
		return res
	}
	order := make([]int8, n)
	copy(order, mv[:n])
	scores := make([]int, n)
	for depth := 1; depth <= maxDepth; depth++ {
		bestI, best := -1, LossScore-1
		alpha := LossScore - 1
		complete := true
		for k, m := range order {
			v := se.minNode(st, m, depth, 1, alpha, WinScore+1)
			if se.stop {
				complete = false
				break
			}
			scores[k] = v
			if v > best {
				best, bestI = v, k
			}
			if v > alpha {
				alpha = v
			}
		}
		if bestI >= 0 && (complete || bestI > 0) {
			res.Move, res.Score, res.Depth = int(order[bestI]), best, depth
		}
		if !complete {
			break
		}
		// сортировка ходов по оценке этой итерации (вставками, ходов не больше 4)
		for a := 1; a < n; a++ {
			for b := a; b > 0 && scores[b] > scores[b-1]; b-- {
				scores[b], scores[b-1] = scores[b-1], scores[b]
				order[b], order[b-1] = order[b-1], order[b]
			}
		}
		if best >= WinScore-1000 {
			break // выигрыш найден
		}
		if se.MaxNodes == 0 && time.Until(se.Deadline) < 0 {
			break
		}
	}
	res.Nodes = se.Nodes
	return res
}

func (se *Searcher) terminal(st *State, ply int) (int, bool) {
	me := &st.Snakes[0]
	opp := 0
	for j := 1; j < int(st.N); j++ {
		if st.Snakes[j].Alive {
			opp++
		}
	}
	if !me.Alive {
		if opp == 0 {
			return DrawScore, true
		}
		return LossScore + ply*100 - opp*10, true
	}
	if opp == 0 {
		return WinScore - ply, true
	}
	return 0, false
}

func (se *Searcher) maxNode(st *State, depth, ply, alpha, beta int) int {
	se.Nodes++
	if se.timeUp() {
		return 0
	}
	if v, ok := se.terminal(st, ply); ok {
		return v
	}
	if depth == 0 {
		return se.Eval(st)
	}
	mv, n := se.G.SafeMoves(st, 0)
	best := LossScore - 1
	for k := 0; k < n; k++ {
		v := se.minNode(st, mv[k], depth, ply, alpha, beta)
		if se.stop {
			return 0
		}
		if v > best {
			best = v
		}
		if v > alpha {
			alpha = v
		}
		if alpha >= beta {
			break
		}
	}
	return best
}

// minNode перебирает совместные ходы ближайших соперников при нашем ходе m.
func (se *Searcher) minNode(st *State, m int8, depth, ply, alpha, beta int) int {
	g := se.G
	var moves [MaxSnakes]int8
	moves[0] = m
	for _, j := range se.others {
		if st.Snakes[j].Alive {
			moves[j] = se.greedy(st, j)
		}
	}
	var lists [MaxSnakes][4]int8
	var cnt [MaxSnakes]int
	var advAlive [MaxSnakes]int
	na := 0
	myNext := g.Nbr(st.Snakes[0].Head(), int(m))
	for _, j := range se.adv {
		if !st.Snakes[j].Alive {
			continue
		}
		l, c := g.SafeMoves(st, j)
		// сначала ходы, ближе к нашей будущей голове: так чаще отсечение
		if myNext >= 0 {
			for a := 1; a < c; a++ {
				for b := a; b > 0 && g.Dist(g.Nbr(st.Snakes[j].Head(), int(l[b])), myNext) < g.Dist(g.Nbr(st.Snakes[j].Head(), int(l[b-1])), myNext); b-- {
					l[b], l[b-1] = l[b-1], l[b]
				}
			}
		}
		lists[na], cnt[na] = l, c
		advAlive[na] = j
		na++
	}
	best := WinScore + 1
	sawDraw := false
	var idx [MaxSnakes]int
	for {
		for a := 0; a < na; a++ {
			moves[advAlive[a]] = lists[a][idx[a]]
		}
		child := *st
		g.Step(&child, &moves)
		v := se.maxNode(&child, depth-1, ply+1, alpha, beta)
		if se.stop {
			return 0
		}
		if v == DrawScore && !child.Snakes[0].Alive {
			sawDraw = true
		} else if v < best {
			best = v
		}
		if v < beta {
			beta = v
		}
		if alpha >= beta {
			return best
		}
		// следующая комбинация
		a := 0
		for ; a < na; a++ {
			idx[a]++
			if idx[a] < cnt[a] {
				break
			}
			idx[a] = 0
		}
		if a == na {
			break
		}
	}
	if sawDraw && (best > se.P.DrawOK || best == WinScore+1) && DrawScore < best {
		best = DrawScore
	}
	return best
}

// greedy — простой ход для соперника вне перебора: безопасный ход с наибольшим числом свободных соседей.
func (se *Searcher) greedy(st *State, j int) int8 {
	g := se.G
	mv, n := g.SafeMoves(st, j)
	if n == 1 {
		return mv[0]
	}
	free := g.Full.AndNot(st.Occ).AndNot(g.Static)
	bestD, bestS := mv[0], -1
	for k := 0; k < n; k++ {
		c := g.Nbr(st.Snakes[j].Head(), int(mv[k]))
		var m BB
		m.Set(c)
		s := g.Neighbors(m).And(free).Count() * 2
		if st.Food.Has(c) && st.Snakes[j].Health < 50 {
			s += 3
		}
		if s > bestS {
			bestS, bestD = s, mv[k]
		}
	}
	return bestD
}

// Eval — оценка позиции для змейки 0: территория по одновременному BFS с учётом
// освобождения хвостов, длина, еда, здоровье, запертость.
func (se *Searcher) Eval(st *State) int {
	g, p := se.G, se.P
	n := int(st.N)
	var owned [MaxSnakes]BB
	var freeAt [maxFree]BB
	blocked := st.Occ.Or(g.Static)
	claimed := BB{}
	maxT := 0
	if !g.Constrictor {
		var seen BB
		for i := 0; i < n; i++ {
			s := &st.Snakes[i]
			if !s.Alive {
				continue
			}
			L := int(s.Len)
			for k := 0; k < L; k++ {
				c := s.Seg(k)
				if seen.Has(c) {
					continue
				}
				seen.Set(c)
				t := L - k
				if t < maxFree {
					freeAt[t].Set(c)
					if t > maxT {
						maxT = t
					}
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		if st.Snakes[i].Alive {
			owned[i].Set(st.Snakes[i].Head())
			claimed.Set(st.Snakes[i].Head())
		}
	}
	foodDist := 99
	if st.Food.And(owned[0]).Count() > 0 {
		foodDist = 0
	}
	for d := 1; d < 64; d++ {
		if d < maxFree {
			blocked = blocked.AndNot(freeAt[d])
		}
		avail := g.Full.AndNot(blocked).AndNot(claimed)
		var exp [MaxSnakes]BB
		grew := false
		for i := 0; i < n; i++ {
			if st.Snakes[i].Alive {
				exp[i] = g.Neighbors(owned[i]).And(avail)
			}
		}
		for i := 0; i < n; i++ {
			if exp[i].Zero() {
				continue
			}
			for j := i + 1; j < n; j++ {
				both := exp[i].And(exp[j])
				if both.Zero() {
					continue
				}
				li, lj := st.Snakes[i].Len, st.Snakes[j].Len
				switch {
				case li > lj:
					exp[j] = exp[j].AndNot(both)
				case lj > li:
					exp[i] = exp[i].AndNot(both)
				default:
					exp[i] = exp[i].AndNot(both)
					exp[j] = exp[j].AndNot(both)
					claimed = claimed.Or(both)
				}
			}
		}
		for i := 0; i < n; i++ {
			if !exp[i].Zero() {
				owned[i] = owned[i].Or(exp[i])
				claimed = claimed.Or(exp[i])
				grew = true
			}
		}
		if foodDist == 99 && !exp[0].And(st.Food).Zero() {
			foodDist = d
		}
		if !grew && d >= maxT {
			break
		}
	}
	me := &st.Snakes[0]
	myT := owned[0].Count()*10 - owned[0].And(g.HazardBB).Count()*p.HazardPen
	oppT, oppL, alive := 0, 0, 0
	for j := 1; j < n; j++ {
		o := &st.Snakes[j]
		if !o.Alive {
			continue
		}
		alive++
		t := owned[j].Count()*10 - owned[j].And(g.HazardBB).Count()*p.HazardPen
		if t > oppT {
			oppT = t
		}
		if int(o.Len) > oppL {
			oppL = int(o.Len)
		}
	}
	score := p.Terr * (myT - oppT) / 10
	myL := int(me.Len)
	dl := myL - oppL
	cl := dl
	if cl > p.LenCap {
		cl = p.LenCap
	} else if cl < -p.LenCap {
		cl = -p.LenCap
	}
	score += p.Len*cl + p.LenExtra*(dl-cl)
	if !g.Constrictor {
		if foodDist < 99 && (int(me.Health) < p.Hungry || dl < p.LenWant) {
			score -= p.FoodDist * foodDist
		}
		if !st.Food.Zero() && foodDist > int(me.Health) {
			score -= p.Starve
		}
		score += p.Health * int(me.Health) / 10
	}
	if myT/10 < myL {
		score -= p.Trap * (myL - myT/10)
	}
	for j := 1; j < n; j++ {
		o := &st.Snakes[j]
		if o.Alive && owned[j].Count() < int(o.Len) {
			score += p.Trap * (int(o.Len) - owned[j].Count()) / 2
		}
	}
	score += p.Kill * (n - 1 - alive)
	return score
}
