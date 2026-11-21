package engine

import (
	"math/rand"
	"sync"
)

// Хэш позиции по Зобристу и таблица уже посчитанных позиций.
type zobrist struct {
	head, body, tail [MaxSnakes][MaxCells]uint64
	length           [MaxSnakes][256]uint64
	hp               [MaxSnakes][128]uint64
	dead             [MaxSnakes]uint64
	food             [MaxCells]uint64
	move             [4]uint64
}

var Z = func() *zobrist {
	r := rand.New(rand.NewSource(20261121))
	z := &zobrist{}
	for i := 0; i < MaxSnakes; i++ {
		for c := 0; c < MaxCells; c++ {
			z.head[i][c], z.body[i][c], z.tail[i][c] = r.Uint64(), r.Uint64(), r.Uint64()
		}
		for k := range z.length[i] {
			z.length[i][k] = r.Uint64()
		}
		for k := range z.hp[i] {
			z.hp[i][k] = r.Uint64()
		}
		z.dead[i] = r.Uint64()
	}
	for c := range z.food {
		z.food[c] = r.Uint64()
	}
	for m := range z.move {
		z.move[m] = r.Uint64()
	}
	return z
}()

func (st *State) Hash() uint64 {
	var h uint64
	for i := 0; i < int(st.N); i++ {
		s := &st.Snakes[i]
		if !s.Alive {
			h ^= Z.dead[i]
			continue
		}
		h ^= Z.head[i][s.Head()] ^ Z.tail[i][s.Tail()] ^ Z.length[i][s.Len] ^ Z.hp[i][s.Health&127]
		for k := 1; k < int(s.Len)-1; k++ {
			h ^= Z.body[i][s.Seg(k)]
		}
	}
	for m := st.Food; !m.Zero(); {
		c := m.First()
		m.Clr(c)
		h ^= Z.food[c]
	}
	return h
}

const (
	ttExact = 1
	ttLower = 2
	ttUpper = 3
	ttBits  = 18
)

type ttEntry struct {
	key   uint64
	val   int32
	depth int8
	flag  int8
	move  uint8
	gen   uint8
}

type TT struct {
	e   []ttEntry
	gen uint8
}

var ttPool = sync.Pool{New: func() any { return &TT{e: make([]ttEntry, 1<<ttBits)} }}

// GetTT берёт таблицу из пула; старые записи отсекаются по поколению.
func GetTT() *TT {
	t := ttPool.Get().(*TT)
	t.gen++
	if t.gen == 0 {
		clear(t.e)
		t.gen = 1
	}
	return t
}

func PutTT(t *TT) { ttPool.Put(t) }

func (t *TT) probe(key uint64) *ttEntry {
	e := &t.e[key&(1<<ttBits-1)]
	if e.gen == t.gen && e.key == key {
		return e
	}
	return nil
}

func (t *TT) store(key uint64, val, depth int, flag int8, move uint8) {
	e := &t.e[key&(1<<ttBits-1)]
	if e.gen == t.gen && e.key != key && int(e.depth) > depth+2 {
		return
	}
	*e = ttEntry{key: key, val: int32(val), depth: int8(depth), flag: flag, move: move, gen: t.gen}
}
