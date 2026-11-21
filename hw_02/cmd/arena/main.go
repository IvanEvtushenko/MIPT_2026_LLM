// Локальная арена: матчи между версиями змейки, простыми ботами и внешними змейками по HTTP.
// Режимы: обычное сравнение A против B с SPRT и подбор весов SPSA.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"battlesnake/engine"
)

type Agent interface {
	Move(g *engine.Geom, st *engine.State, i int, rules *engine.Rules, gameID string, rng *rand.Rand) int
	Start(g *engine.Geom, st *engine.State, i int, rules *engine.Rules, gameID string)
	End(g *engine.Geom, st *engine.State, i int, rules *engine.Rules, gameID string)
}

var (
	moveMs   = flag.Int("ms", 40, "время на ход для нашей змейки, мс")
	nodes    = flag.Int64("nodes", 0, "лимит узлов на ход вместо времени")
	httpTO   = flag.Int("httptimeout", 500, "timeout в запросе к внешним змейкам, мс")
	maxDepth = flag.Int("depth", 60, "максимальная глубина")
)

type engAgent struct{ p engine.Params }

func (a *engAgent) Move(g *engine.Geom, st *engine.State, i int, _ *engine.Rules, _ string, _ *rand.Rand) int {
	cp := *st
	cp.Swap(0, i)
	se := &engine.Searcher{G: g, P: &a.p, MaxNodes: *nodes, Deadline: time.Now().Add(time.Duration(*moveMs) * time.Millisecond)}
	return se.Search(&cp, *maxDepth).Move
}
func (a *engAgent) Start(*engine.Geom, *engine.State, int, *engine.Rules, string) {}
func (a *engAgent) End(*engine.Geom, *engine.State, int, *engine.Rules, string)   {}

type botAgent struct{ kind string }

func (a *botAgent) Move(g *engine.Geom, st *engine.State, i int, _ *engine.Rules, _ string, rng *rand.Rand) int {
	return engine.BotMove(a.kind, g, st, i, rng)
}
func (a *botAgent) Start(*engine.Geom, *engine.State, int, *engine.Rules, string) {}
func (a *botAgent) End(*engine.Geom, *engine.State, int, *engine.Rules, string)   {}

type httpAgent struct {
	url  string
	cl   *http.Client
	mu   sync.Mutex
	last map[string]int
}

func (a *httpAgent) post(path string, req *engine.Request) ([]byte, error) {
	b, _ := json.Marshal(req)
	resp, err := a.cl.Post(a.url+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return buf.Bytes(), nil
}

func (a *httpAgent) Move(g *engine.Geom, st *engine.State, i int, rules *engine.Rules, gameID string, _ *rand.Rand) int {
	key := fmt.Sprintf("%s/%d", gameID, i)
	a.mu.Lock()
	last := a.last[key]
	a.mu.Unlock()
	b, err := a.post("/move", engine.ToRequest(g, st, i, rules, gameID, *httpTO))
	mv := last
	if err == nil {
		var r struct{ Move string }
		if json.Unmarshal(b, &r) == nil {
			for d, n := range engine.MoveNames {
				if n == r.Move {
					mv = d
				}
			}
		}
	}
	a.mu.Lock()
	a.last[key] = mv
	a.mu.Unlock()
	return mv
}

func (a *httpAgent) Start(g *engine.Geom, st *engine.State, i int, rules *engine.Rules, gameID string) {
	a.post("/start", engine.ToRequest(g, st, i, rules, gameID, *httpTO))
}

func (a *httpAgent) End(g *engine.Geom, st *engine.State, i int, rules *engine.Rules, gameID string) {
	a.post("/end", engine.ToRequest(g, st, i, rules, gameID, *httpTO))
	a.mu.Lock()
	delete(a.last, fmt.Sprintf("%s/%d", gameID, i))
	a.mu.Unlock()
}

// parseParams: «Terr=12,Len=30» или путь к JSON поверх весов по умолчанию.
func parseParams(spec string) engine.Params {
	p := engine.DefaultParams
	if spec == "" {
		return p
	}
	if strings.HasSuffix(spec, ".json") {
		b, err := os.ReadFile(spec)
		if err != nil {
			panic(err)
		}
		if err := json.Unmarshal(b, &p); err != nil {
			panic(err)
		}
		return p
	}
	v := reflect.ValueOf(&p).Elem()
	for _, kv := range strings.Split(spec, ",") {
		parts := strings.SplitN(kv, "=", 2)
		f := v.FieldByName(parts[0])
		n, err := strconv.Atoi(parts[1])
		if !f.IsValid() || err != nil {
			panic("bad param " + kv)
		}
		f.SetInt(int64(n))
	}
	return p
}

func newAgent(spec string) Agent {
	switch {
	case strings.HasPrefix(spec, "http"):
		return &httpAgent{url: strings.TrimRight(spec, "/"), cl: &http.Client{Timeout: time.Duration(*httpTO+300) * time.Millisecond}, last: map[string]int{}}
	case strings.HasPrefix(spec, "bot:"):
		return &botAgent{kind: spec[4:]}
	case spec == "eng":
		return &engAgent{p: engine.DefaultParams}
	case strings.HasPrefix(spec, "eng:"):
		return &engAgent{p: parseParams(spec[4:])}
	}
	panic("unknown agent " + spec)
}

// play возвращает ход выбывания каждой змейки (1<<30 — дожила до конца) и число ходов.
func play(mode string, agents []Agent, seed int64) ([]int, int) {
	rng := rand.New(rand.NewSource(seed))
	botRng := rand.New(rand.NewSource(seed ^ 0x5eed))
	n := len(agents)
	g, st, rules := engine.NewGame(mode, n, rng)
	gameID := fmt.Sprintf("local-%d-%d", seed, time.Now().UnixNano())
	for i, a := range agents {
		a.Start(g, st, i, rules, gameID)
	}
	death := make([]int, n)
	for i := range death {
		death[i] = 1 << 30
	}
	for st.Turn < 1500 {
		alive := st.AliveCount()
		if (n == 2 && alive < 2) || (n > 2 && alive <= 1) || alive == 0 {
			break
		}
		var moves [engine.MaxSnakes]int8
		for i := 0; i < n; i++ {
			if st.Snakes[i].Alive {
				moves[i] = int8(agents[i].Move(g, st, i, rules, gameID, botRng))
			}
		}
		was := make([]bool, n)
		for i := 0; i < n; i++ {
			was[i] = st.Snakes[i].Alive
		}
		g.Step(st, &moves)
		rules.After(g, st, rng)
		for i := 0; i < n; i++ {
			if was[i] && !st.Snakes[i].Alive {
				death[i] = int(st.Turn)
			}
		}
	}
	for i, a := range agents {
		a.End(g, st, i, rules, gameID)
	}
	return death, int(st.Turn)
}

// score — результат змейки i в [0,1] по ходу выбывания: +1 за каждого, кто выбыл раньше, +0.5 за одновременных.
func score(death []int, i int) float64 {
	s := 0.0
	for j := range death {
		if j == i {
			continue
		}
		if death[j] < death[i] {
			s++
		} else if death[j] == death[i] {
			s += 0.5
		}
	}
	return s / float64(len(death)-1)
}

func elo(s float64) float64 {
	if s <= 0 {
		return -999
	}
	if s >= 1 {
		return 999
	}
	return -400 * math.Log10(1/s-1)
}

func llr(w, d, l int, e0, e1 float64) float64 {
	n := float64(w + d + l)
	if n == 0 || w+d == 0 || d+l == 0 {
		return 0
	}
	s := (float64(w) + float64(d)/2) / n
	v := (float64(w)*(1-s)*(1-s) + float64(d)*(0.5-s)*(0.5-s) + float64(l)*s*s) / n
	s0 := 1 / (1 + math.Pow(10, -e0/400))
	s1 := 1 / (1 + math.Pow(10, -e1/400))
	return n * (s1 - s0) * (2*s - s0 - s1) / (2 * v)
}

func main() {
	a := flag.String("a", "eng", "змейка A")
	b := flag.String("b", "bot:greedy", "змейка B (в режимах на 4 — три копии)")
	mode := flag.String("mode", "duel", "duel|standard|royale|constrictor")
	games := flag.Int("games", 200, "сколько игр максимум")
	workers := flag.Int("workers", 10, "параллельных игр")
	seed := flag.Int64("seed", 1, "начальное зерно")
	sprt := flag.String("sprt", "", "границы Эло «0,10»: остановиться, когда тест решит")
	spsa := flag.String("spsa", "", "подбор весов: «Terr:3,Len:8,...» (имя:шаг)")
	out := flag.String("out", "", "куда писать веса SPSA")
	flag.Parse()

	if *spsa != "" {
		runSPSA(*spsa, *mode, *games, *workers, *seed, *out)
		return
	}
	var e0, e1 float64
	useSPRT := false
	if *sprt != "" {
		fmt.Sscanf(*sprt, "%g,%g", &e0, &e1)
		useSPRT = true
	}
	nPlayers := 2
	if *mode != "duel" {
		nPlayers = 4
	}
	A, B := newAgent(*a), newAgent(*b)
	var mu sync.Mutex
	w, d, l, sum, cnt, turns := 0, 0, 0, 0.0, 0, 0
	done := false
	jobs := make(chan int)
	var wg sync.WaitGroup
	t0 := time.Now()
	for k := 0; k < *workers; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for gi := range jobs {
				// пары игр с одним зерном и переставленными местами
				s := *seed + int64(gi/2)
				agents := make([]Agent, nPlayers)
				seatA := gi % 2
				if nPlayers > 2 {
					seatA = gi % nPlayers
				}
				for i := range agents {
					agents[i] = B
				}
				agents[seatA] = A
				places, tn := play(*mode, agents, s)
				sc := score(places, seatA)
				mu.Lock()
				cnt++
				sum += sc
				turns += tn
				switch {
				case sc > 0.75:
					w++
				case sc < 0.25:
					l++
				default:
					d++
				}
				if useSPRT {
					if r := llr(w, d, l, e0, e1); (r > 2.94 || r < -2.94) && cnt >= 20 {
						done = true
					}
				}
				if cnt%50 == 0 {
					fmt.Printf("  %d игр: W%d D%d L%d score=%.3f elo=%+.0f llr=%.2f %.0fs\n", cnt, w, d, l, sum/float64(cnt),
						elo(sum/float64(cnt)), llr(w, d, l, e0, e1), time.Since(t0).Seconds())
				}
				mu.Unlock()
			}
		}()
	}
	for gi := 0; gi < *games; gi++ {
		mu.Lock()
		stop := done
		mu.Unlock()
		if stop {
			break
		}
		jobs <- gi
	}
	close(jobs)
	wg.Wait()
	s := sum / float64(cnt)
	se := math.Sqrt(s*(1-s)/float64(cnt)) * 1.96
	verdict := ""
	if useSPRT {
		r := llr(w, d, l, e0, e1)
		verdict = fmt.Sprintf(" llr=%.2f", r)
		if r > 2.94 {
			verdict += " ACCEPT"
		} else if r < -2.94 {
			verdict += " REJECT"
		}
	}
	fmt.Printf("RESULT %s vs %s mode=%s n=%d W%d D%d L%d score=%.3f±%.3f elo=%+.0f [%+.0f,%+.0f] turns=%.0f %.0fs%s\n",
		*a, *b, *mode, cnt, w, d, l, s, se, elo(s), elo(s-se), elo(s+se), float64(turns)/float64(cnt), time.Since(t0).Seconds(), verdict)
}

func runSPSA(spec, mode string, iters, workers int, seed int64, out string) {
	type pp struct {
		name string
		c    float64
	}
	var ps []pp
	for _, kv := range strings.Split(spec, ",") {
		parts := strings.SplitN(kv, ":", 2)
		c, _ := strconv.ParseFloat(parts[1], 64)
		ps = append(ps, pp{parts[0], c})
	}
	base := engine.DefaultParams
	if f := os.Getenv("SPSA_START"); f != "" {
		base = parseParams(f)
	}
	theta := make([]float64, len(ps))
	bv := reflect.ValueOf(&base).Elem()
	for i, p := range ps {
		theta[i] = float64(bv.FieldByName(p.name).Int())
	}
	build := func(th []float64) engine.Params {
		p := base
		v := reflect.ValueOf(&p).Elem()
		for i, q := range ps {
			v.FieldByName(q.name).SetInt(int64(math.Round(th[i])))
		}
		return p
	}
	var mu sync.Mutex
	it := 0
	var wg sync.WaitGroup
	t0 := time.Now()
	for k := 0; k < workers; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed + int64(k)*7919))
			for {
				mu.Lock()
				if it >= iters {
					mu.Unlock()
					return
				}
				it++
				myIt := it
				delta := make([]float64, len(ps))
				plus, minus := make([]float64, len(ps)), make([]float64, len(ps))
				for i := range ps {
					delta[i] = float64(rng.Intn(2)*2 - 1)
					plus[i] = theta[i] + ps[i].c*delta[i]
					minus[i] = theta[i] - ps[i].c*delta[i]
				}
				mu.Unlock()
				Ap, Am := &engAgent{p: build(plus)}, &engAgent{p: build(minus)}
				s := seed*100000 + int64(myIt)
				r := 0.0
				for seat := 0; seat < 2; seat++ {
					agents := []Agent{Ap, Am}
					if mode != "duel" {
						agents = []Agent{Ap, Am, Ap, Am}
					}
					if seat == 1 {
						for i := range agents {
							if agents[i] == Ap {
								agents[i] = Am
							} else {
								agents[i] = Ap
							}
						}
					}
					places, _ := play(mode, agents, s)
					for i, ag := range agents {
						if ag == Ap {
							r += score(places, i)
						} else {
							r -= score(places, i)
						}
					}
				}
				if mode != "duel" {
					r /= 2
				}
				r /= 2 // в [-1,1]
				mu.Lock()
				lr := 0.25
				for i := range ps {
					theta[i] += lr * ps[i].c * r * delta[i]
				}
				if it%100 == 0 {
					p := build(theta)
					b, _ := json.Marshal(p)
					fmt.Printf("spsa it=%d %.0fs %s\n", it, time.Since(t0).Seconds(), b)
					if out != "" {
						os.WriteFile(out, b, 0644)
					}
				}
				mu.Unlock()
			}
		}(k)
	}
	wg.Wait()
	p := build(theta)
	b, _ := json.Marshal(p)
	fmt.Printf("spsa final %s\n", b)
	if out != "" {
		os.WriteFile(out, b, 0644)
	}
}
