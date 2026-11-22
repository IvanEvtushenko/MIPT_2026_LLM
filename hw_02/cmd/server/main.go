// Сервер змейки по Battlesnake API: GET /, POST /start, /move, /end, плюс GET /stats.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"battlesnake/engine"

	"golang.org/x/crypto/acme/autocert"
)

var (
	mu    sync.Mutex
	games = map[string]*gameInfo{}
	stats = map[string]*[3]int{} // режим -> победы, поражения, ничьи
)

type gameInfo struct {
	lastProc time.Duration
	netEst   float64 // оценка сетевой задержки, мс
	seen     time.Time
	mode     string
}

func info(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{
		"apiversion": "1", "author": "IvanEvtushenko", "color": "#7b3fe4",
		"head": "default", "tail": "default", "version": "v1",
	})
}

func latencyMs(v any) (float64, bool) {
	switch x := v.(type) {
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil && f > 0
	case float64:
		return x, x > 0
	}
	return 0, false
}

func move(w http.ResponseWriter, r *http.Request) {
	t0 := time.Now()
	var req engine.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	timeout := req.Game.Timeout
	if timeout <= 0 {
		timeout = 500
	}
	mu.Lock()
	gi := games[req.Game.ID]
	if gi == nil {
		gi = &gameInfo{netEst: 120}
		games[req.Game.ID] = gi
	}
	if lat, ok := latencyMs(req.You.Latency); ok && gi.lastProc > 0 {
		net := lat - float64(gi.lastProc.Milliseconds())
		if net < 0 {
			net = 0
		}
		gi.netEst = 0.7*gi.netEst + 0.3*net
		if net > gi.netEst {
			gi.netEst = net // берём худший недавний случай
		}
	}
	gi.seen = time.Now()
	netEst := gi.netEst
	mu.Unlock()

	budget := time.Duration(float64(timeout)-netEst-90) * time.Millisecond
	if budget > 380*time.Millisecond {
		budget = 380 * time.Millisecond
	}
	if budget < 25*time.Millisecond {
		budget = 25 * time.Millisecond
	}
	mv, depth, nodes := "up", 0, int64(0)
	if g, st, ok := engine.Build(&req); ok {
		p := engine.Profiles[engine.ModeOf(req.Game.Ruleset.Name, len(req.Board.Snakes))]
		se := &engine.Searcher{G: g, P: &p, Deadline: t0.Add(budget)}
		res := se.Search(st, 60)
		mv, depth, nodes = engine.MoveNames[res.Move], res.Depth, res.Nodes
	} else {
		mv = engine.Fallback(&req)
	}
	json.NewEncoder(w).Encode(map[string]string{"move": mv})
	proc := time.Since(t0)
	mu.Lock()
	gi.lastProc = proc
	mu.Unlock()
	fmt.Printf("move g=%s rs=%s t=%d mv=%s d=%d n=%d ms=%d net=%.0f\n",
		short(req.Game.ID), req.Game.Ruleset.Name, req.Turn, mv, depth, nodes, proc.Milliseconds(), netEst)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func start(w http.ResponseWriter, r *http.Request) {
	var req engine.Request
	json.NewDecoder(r.Body).Decode(&req)
	mu.Lock()
	games[req.Game.ID] = &gameInfo{netEst: 120, seen: time.Now(), mode: fmt.Sprintf("%s%d", req.Game.Ruleset.Name, len(req.Board.Snakes))}
	mu.Unlock()
	fmt.Printf("start g=%s rs=%s map=%s n=%d timeout=%d\n", req.Game.ID, req.Game.Ruleset.Name, req.Game.Map,
		len(req.Board.Snakes), req.Game.Timeout)
	w.Write([]byte("{}"))
}

func end(w http.ResponseWriter, r *http.Request) {
	var req engine.Request
	json.NewDecoder(r.Body).Decode(&req)
	res := 1 // поражение
	alive := false
	for _, s := range req.Board.Snakes {
		if s.ID == req.You.ID {
			alive = true
		}
	}
	if alive && len(req.Board.Snakes) == 1 {
		res = 0
	} else if alive {
		res = 2
	} else if len(req.Board.Snakes) == 0 {
		res = 2
	}
	mu.Lock()
	mode := req.Game.Ruleset.Name
	if gi := games[req.Game.ID]; gi != nil && gi.mode != "" {
		mode = gi.mode
	}
	if stats[mode] == nil {
		stats[mode] = &[3]int{}
	}
	stats[mode][res]++
	delete(games, req.Game.ID)
	for id, gi := range games {
		if time.Since(gi.seen) > 30*time.Minute {
			delete(games, id)
		}
	}
	mu.Unlock()
	fmt.Printf("end g=%s rs=%s turn=%d result=%s\n", req.Game.ID, req.Game.Ruleset.Name, req.Turn, [3]string{"win", "loss", "draw"}[res])
	w.Write([]byte("{}"))
}

func statsH(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	json.NewEncoder(w).Encode(stats)
}

func main() {
	httpAddr := flag.String("http", ":8443", "адрес без TLS")
	httpsAddr := flag.String("https", "", "адрес с TLS (например :443), пусто — без TLS")
	host := flag.String("host", "", "домен для сертификата Let's Encrypt")
	cache := flag.String("cache", "certs", "папка для сертификатов")
	paramsFile := flag.String("params", "", "JSON с весами оценки")
	flag.Parse()
	if *paramsFile != "" {
		b, err := os.ReadFile(*paramsFile)
		if err != nil {
			log.Fatal(err)
		}
		for m, p := range engine.Profiles {
			if err := json.Unmarshal(b, &p); err != nil {
				log.Fatal(err)
			}
			engine.Profiles[m] = p
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", info)
	mux.HandleFunc("/start", start)
	mux.HandleFunc("/move", move)
	mux.HandleFunc("/end", end)
	mux.HandleFunc("/stats", statsH)
	if *httpsAddr != "" && *host != "" {
		m := &autocert.Manager{Prompt: autocert.AcceptTOS, HostPolicy: autocert.HostWhitelist(*host), Cache: autocert.DirCache(*cache)}
		srv := &http.Server{Addr: *httpsAddr, Handler: mux, TLSConfig: m.TLSConfig(),
			ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 120 * time.Second}
		go func() { log.Fatal(srv.ListenAndServeTLS("", "")) }()
	}
	srv := &http.Server{Addr: *httpAddr, Handler: mux, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 120 * time.Second}
	log.Printf("listening http=%s https=%s host=%s", *httpAddr, *httpsAddr, *host)
	log.Fatal(srv.ListenAndServe())
}
