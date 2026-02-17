package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	baseURL              string
	urls                 string   // comma-separated; if set we round-robin across these instead of single baseURL
	urlList              []string
	urlIndex             atomic.Uint64
	forceSingleURL       atomic.Bool // when true, getBaseURL always returns first URL (write-stress single-node)
	insertsPS   float64
	updatesPS   float64
	deletesPS   float64
	readsPS     float64
	insertsOnly bool

	// Stress test flags
	stressMode           bool
	stressStep           float64
	stressDuration       time.Duration
	stressErrorThreshold float64

	// Write-stress test flags (separate mode: ramp writes until p95 latency hits limit)
	writeStressMode         bool
	writeStressInitial      float64
	writeStressStep         float64
	writeStressDuration     time.Duration
	writeStressP95LimitMs   float64
	writeStressConsistency  string // "strong" or "eventual"
	writeStressSingleNode   bool   // when true, send all writes to first URL only (no round-robin)
)

var (
	client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        300,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	insertCnt atomic.Int64
	updateCnt atomic.Int64
	deleteCnt atomic.Int64
	readCnt   atomic.Int64
	errCnt    atomic.Int64
)

const (
	usersTable    = "users"
	productsTable = "products"
	ordersTable   = "orders"
)

var productNames = []string{"Laptop", "Phone", "Tablet", "Headphones", "Monitor", "Keyboard", "Mouse", "Webcam", "Speaker", "Charger"}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `trafficgen - Generate live insert/update/delete/read traffic for HedgehogDB demo tables (users, products, orders).

Usage: trafficgen [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Environment:
  HEDGEHOG_URL  Base URL if -url is not set (default: http://localhost:8081, first cluster node)

Example:
  ./bin/trafficgen
  ./bin/trafficgen -inserts-only
  ./bin/trafficgen -inserts 10 -updates 3 -deletes 1 -reads 20
  ./bin/trafficgen -urls http://127.0.0.1:8081,http://127.0.0.1:8082,http://127.0.0.1:8083
  ./bin/trafficgen -stress
  ./bin/trafficgen -stress -stress-step 200 -stress-duration 15s -stress-error-threshold 0.02
  ./bin/trafficgen -write-stress
  ./bin/trafficgen -write-stress -write-stress-initial 50 -write-stress-step 25 -write-stress-duration 30s -write-stress-p95-limit 500
  ./bin/trafficgen -write-stress -write-stress-consistency eventual -write-stress-single-node -url http://127.0.0.1:8081
`)
	}
	flag.StringVar(&baseURL, "url", getEnv("HEDGEHOG_URL", "http://localhost:8081"), "HedgehogDB base URL (used when -urls is not set; default 8081 = first cluster node)")
	flag.StringVar(&urls, "urls", "", "comma-separated node URLs to round-robin across (overrides -url)")
	flag.Float64Var(&insertsPS, "inserts", 5, "inserts per second (across all tables; keep > updates+deletes so data grows)")
	flag.Float64Var(&updatesPS, "updates", 2, "updates per second (across all tables)")
	flag.Float64Var(&deletesPS, "deletes", 1, "deletes per second (across all tables)")
	flag.Float64Var(&readsPS, "reads", 10, "reads per second (across all tables)")
	flag.BoolVar(&insertsOnly, "inserts-only", false, "inserts only mode (no updates, deletes, or reads)")
	flag.BoolVar(&stressMode, "stress", false, "enable ramp-up stress test mode")
	flag.Float64Var(&stressStep, "stress-step", 100, "req/s added each stress step")
	flag.DurationVar(&stressDuration, "stress-duration", 10*time.Second, "how long to hold each rate before ramping")
	flag.Float64Var(&stressErrorThreshold, "stress-error-threshold", 0.05, "error percentage that triggers stop (0.05 = 5%)")
	flag.BoolVar(&writeStressMode, "write-stress", false, "write-stress mode: ramp writes until p95 latency hits limit, then report benchmark")
	flag.Float64Var(&writeStressInitial, "write-stress-initial", 50, "initial write rate (writes/s) for write-stress")
	flag.Float64Var(&writeStressStep, "write-stress-step", 25, "write rate increment per step (writes/s)")
	flag.DurationVar(&writeStressDuration, "write-stress-duration", 30*time.Second, "how long to hold each write rate before checking p95")
	flag.Float64Var(&writeStressP95LimitMs, "write-stress-p95-limit", 500, "stop when p95 latency (ms) reaches this; benchmark = previous step rate")
	flag.StringVar(&writeStressConsistency, "write-stress-consistency", "strong", "write consistency: strong (quorum) or eventual (single-node, no quorum wait)")
	flag.BoolVar(&writeStressSingleNode, "write-stress-single-node", false, "send all write-stress traffic to first URL only (use -url for max single-node throughput)")
	flag.Parse()

	if insertsOnly {
		updatesPS = 0
		deletesPS = 0
		readsPS = 0
		log.Printf("inserts-only mode: updates=0, deletes=0, reads=0")
	}

	// Parse -urls for round-robin; otherwise single baseURL
	if urls != "" {
		for _, u := range strings.Split(urls, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				urlList = append(urlList, strings.TrimSuffix(u, "/"))
			}
		}
	}
	if len(urlList) == 0 {
		urlList = []string{strings.TrimSuffix(baseURL, "/")}
	}

	if stressMode {
		log.Printf("Stress test | base inserts=%.1f/s updates=%.1f/s deletes=%.1f/s reads=%.1f/s", insertsPS, updatesPS, deletesPS, readsPS)
		log.Printf("Stress test | step=%.0f req/s duration=%v threshold=%.1f%%", stressStep, stressDuration, stressErrorThreshold*100)
	} else if writeStressMode {
		cons := writeStressConsistency
		if cons != "strong" && cons != "eventual" {
			cons = "strong"
			writeStressConsistency = cons
		}
		singleNode := ""
		if writeStressSingleNode {
			singleNode = " single-node"
		}
		log.Printf("Write-stress | %s writes%s; initial=%.0f/s step=%.0f/s duration=%v p95-limit=%.0fms",
			cons, singleNode, writeStressInitial, writeStressStep, writeStressDuration, writeStressP95LimitMs)
	} else {
		log.Printf("Traffic gen | inserts=%.1f/s updates=%.1f/s deletes=%.1f/s reads=%.1f/s", insertsPS, updatesPS, deletesPS, readsPS)
	}
	logNodeTarget()

	tables := []string{usersTable, productsTable, ordersTable}

	// Ensure demo tables exist (idempotent; 409 = already exists)
	if err := ensureTables(tables); err != nil {
		log.Fatalf("Cannot reach server or create tables: %v\nTip: Cluster runs on 8081-8083. Try: ./bin/trafficgen -url http://localhost:8081", err)
	}

	// When we had a single URL, send traffic to all cluster nodes so counts grow on every node
	// (skip when write-stress single-node: we intentionally hit only one node)
	if len(urlList) == 1 && !(writeStressMode && writeStressSingleNode) {
		if all := getClusterURLs(urlList[0]); len(all) > 1 {
			urlList = all
			log.Printf("Sending traffic to all %d nodes (round-robin)", len(urlList))
		}
	}

	pools := make(map[string]*keyPool)
	for _, name := range tables {
		pools[name] = &keyPool{keys: make(map[string]bool), rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	}

	// Seed key pools with existing keys from the server
	for _, name := range tables {
		keys, err := scanKeys(name)
		if err != nil {
			log.Printf("Warning: scan %s: %v (updates/deletes/reads will only use keys created by this run)", name, err)
		} else {
			for _, k := range keys {
				pools[name].add(k)
			}
			log.Printf("Table %s: %d existing keys", name, len(keys))
		}
	}

	// Per-table insert counters for unique new keys
	var insertCountersMu sync.Mutex
	insertCounters := map[string]int{
		usersTable:    1000, // start above seed range (user-001..user-020)
		productsTable: 100,
		ordersTable:   1000,
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	if stressMode {
		runStress(pools, &insertCountersMu, insertCounters, sig)
		return
	}
	if writeStressMode {
		runWriteStress(pools, &insertCountersMu, insertCounters, sig)
		return
	}

	// Normal mode
	stop := make(chan struct{})
	if insertsPS > 0 {
		go runInserts(pools, &insertCountersMu, insertCounters, insertsPS, stop)
	}
	if updatesPS > 0 {
		go runUpdates(pools, updatesPS, stop)
	}
	if deletesPS > 0 {
		go runDeletes(pools, deletesPS, stop)
	}
	if readsPS > 0 {
		go runReads(pools, readsPS, stop)
	}
	go runStats(stop)

	<-sig
	close(stop)
	log.Println("Stopping...")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getBaseURL returns the next URL when round-robin across urlList; otherwise the single URL.
// When forceSingleURL is set (write-stress single-node), always returns the first URL.
func getBaseURL() string {
	if forceSingleURL.Load() || len(urlList) == 1 {
		return bootstrapURL()
	}
	if len(urlList) == 0 {
		return strings.TrimSuffix(baseURL, "/")
	}
	i := urlIndex.Add(1) % uint64(len(urlList))
	return urlList[i]
}

// bootstrapURL returns the first URL for one-off setup (ensure tables, initial scan).
func bootstrapURL() string {
	if len(urlList) == 0 {
		return strings.TrimSuffix(baseURL, "/")
	}
	return urlList[0]
}

// logNodeTarget logs which node(s) we're hitting (single URL or round-robin list; optionally cluster node_id).
func logNodeTarget() {
	if len(urlList) == 0 {
		return
	}
	if len(urlList) == 1 {
		// Optionally fetch cluster status to show node_id
		req, err := http.NewRequest("GET", urlList[0]+"/api/v1/cluster/status", nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				var status struct {
					NodeID   string `json:"node_id"`
					BindAddr string `json:"bind_addr"`
					Total    int    `json:"total_nodes"`
				}
				if json.NewDecoder(resp.Body).Decode(&status) == nil {
					resp.Body.Close()
					log.Printf("Hitting node: %s (%s) [1 of %d nodes]", status.NodeID, status.BindAddr, status.Total)
					return
				}
				resp.Body.Close()
			}
		}
		fromEnv := ""
		if e := os.Getenv("HEDGEHOG_URL"); e != "" && strings.TrimSuffix(e, "/") == urlList[0] {
			fromEnv = " (from HEDGEHOG_URL)"
		}
		log.Printf("Hitting: %s%s", urlList[0], fromEnv)
		return
	}
	// Multiple URLs: show short form (host:port)
	parts := make([]string, 0, len(urlList))
	for _, u := range urlList {
		parsed, err := url.Parse(u)
		if err == nil && parsed.Host != "" {
			parts = append(parts, parsed.Host)
		} else {
			parts = append(parts, u)
		}
	}
	log.Printf("Hitting %d nodes (round-robin): %s", len(urlList), strings.Join(parts, ", "))
}

type keyPool struct {
	mu   sync.Mutex
	keys map[string]bool
	list []string
	rng  *rand.Rand
}

func (p *keyPool) add(k string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.keys[k] {
		p.keys[k] = true
		p.list = append(p.list, k)
	}
}

func (p *keyPool) remove(k string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.keys[k] {
		delete(p.keys, k)
		for i, v := range p.list {
			if v == k {
				p.list = append(p.list[:i], p.list[i+1:]...)
				break
			}
		}
	}
}

func (p *keyPool) random() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.list) == 0 {
		return "", false
	}
	return p.list[p.rng.Intn(len(p.list))], true
}

// takeRandom removes and returns a random key in one atomic op so only one goroutine can get it.
func (p *keyPool) takeRandom() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.list) == 0 {
		return "", false
	}
	i := p.rng.Intn(len(p.list))
	key := p.list[i]
	p.list = append(p.list[:i], p.list[i+1:]...)
	delete(p.keys, key)
	return key, true
}

func (p *keyPool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.list)
}

func scanKeys(table string) ([]string, error) {
	req, err := http.NewRequest("GET", bootstrapURL()+"/api/v1/tables/"+table+"/items", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET %s/items: %d %s", table, resp.StatusCode, string(body))
	}
	var result struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(result.Items))
	for _, it := range result.Items {
		keys = append(keys, it.Key)
	}
	return keys, nil
}

// ---------------------------------------------------------------------------
// Worker pool helpers
// ---------------------------------------------------------------------------

const maxWorkers = 256

func workerCount(rate float64) int {
	n := int(math.Ceil(rate * 0.025))
	if n < 1 {
		n = 1
	}
	if n > maxWorkers {
		n = maxWorkers
	}
	return n
}

func startWorkers(n int) (chan<- func(), *sync.WaitGroup) {
	jobs := make(chan func(), n*2)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fn := range jobs {
				fn()
			}
		}()
	}
	return jobs, &wg
}

// ---------------------------------------------------------------------------
// Worker goroutines – all accept a stop channel for graceful shutdown.
// Each run* function dispatches work to a pool of goroutines so that
// many HTTP requests are in-flight concurrently, actually achieving the
// configured rate rather than being bottlenecked by round-trip latency.
// ---------------------------------------------------------------------------

func runInserts(pools map[string]*keyPool, mu *sync.Mutex, counters map[string]int, rate float64, stop <-chan struct{}) {
	if rate <= 0 {
		return
	}
	nw := workerCount(rate)
	jobs, wg := startWorkers(nw)

	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	tables := []string{usersTable, productsTable, ordersTable}
	idx := 0
	for {
		select {
		case <-stop:
			close(jobs)
			wg.Wait()
			return
		case <-ticker.C:
		}
		table := tables[idx%len(tables)]
		idx++

		mu.Lock()
		counters[table]++
		keyNum := counters[table]
		mu.Unlock()

		tbl, kn := table, keyNum
		select {
		case jobs <- func() {
			var key string
			var body []byte
			switch tbl {
			case usersTable:
				key = fmt.Sprintf("user-%d", kn)
				body = mustJSON(map[string]interface{}{
					"name":  fmt.Sprintf("User %d", kn),
					"email": fmt.Sprintf("user%d@example.com", kn),
					"age":   rand.Intn(40) + 20,
				})
			case productsTable:
				key = fmt.Sprintf("prod-%d", kn)
				body = mustJSON(map[string]interface{}{
					"name":     productNames[rand.Intn(len(productNames))],
					"price":    float64(rand.Intn(900)+100) + 0.99,
					"in_stock": true,
				})
			case ordersTable:
				key = fmt.Sprintf("order-%d", kn)
				body = mustJSON(map[string]interface{}{
					"user_id":    fmt.Sprintf("user-%03d", rand.Intn(20)+1),
					"product_id": fmt.Sprintf("prod-%03d", rand.Intn(10)+1),
					"quantity":   rand.Intn(5) + 1,
					"status":     "completed",
				})
			}
			ok, err := doPut(tbl, key, body)
			if ok {
				insertCnt.Add(1)
				pools[tbl].add(key)
			} else {
				errCnt.Add(1)
				logErr("PUT", tbl, key, err)
			}
		}:
		default:
			// workers saturated — drop this tick
		}
	}
}

func runUpdates(pools map[string]*keyPool, rate float64, stop <-chan struct{}) {
	if rate <= 0 {
		return
	}
	nw := workerCount(rate)
	jobs, wg := startWorkers(nw)

	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	tables := []string{usersTable, productsTable, ordersTable}
	idx := 0
	for {
		select {
		case <-stop:
			close(jobs)
			wg.Wait()
			return
		case <-ticker.C:
		}
		tbl := tables[idx%len(tables)]
		idx++

		select {
		case jobs <- func() {
			key, ok := pools[tbl].random()
			if !ok {
				return
			}
			var body []byte
			switch tbl {
			case usersTable:
				body = mustJSON(map[string]interface{}{
					"name":  fmt.Sprintf("User %s", key),
					"email": fmt.Sprintf("%s@example.com", key),
					"age":   rand.Intn(40) + 20,
				})
			case productsTable:
				body = mustJSON(map[string]interface{}{
					"name":     productNames[rand.Intn(len(productNames))],
					"price":    float64(rand.Intn(900)+100) + 0.99,
					"in_stock": rand.Float32() < 0.8,
				})
			case ordersTable:
				body = mustJSON(map[string]interface{}{
					"user_id":    fmt.Sprintf("user-%03d", rand.Intn(20)+1),
					"product_id": fmt.Sprintf("prod-%03d", rand.Intn(10)+1),
					"quantity":   rand.Intn(5) + 1,
					"status":     []string{"pending", "completed", "shipped"}[rand.Intn(3)],
				})
			}
			ok, err := doPut(tbl, key, body)
			if ok {
				updateCnt.Add(1)
			} else {
				errCnt.Add(1)
				logErr("PUT", tbl, key, err)
			}
		}:
		default:
		}
	}
}

func runDeletes(pools map[string]*keyPool, rate float64, stop <-chan struct{}) {
	if rate <= 0 {
		return
	}
	nw := workerCount(rate)
	jobs, wg := startWorkers(nw)

	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	tables := []string{usersTable, productsTable, ordersTable}
	idx := 0
	for {
		select {
		case <-stop:
			close(jobs)
			wg.Wait()
			return
		case <-ticker.C:
		}
		tbl := tables[idx%len(tables)]
		idx++

		select {
		case jobs <- func() {
			key, ok := pools[tbl].takeRandom()
			if !ok {
				return
			}
			ok, err := doDelete(tbl, key)
			if ok {
				deleteCnt.Add(1)
			} else {
				errCnt.Add(1)
				logErr("DELETE", tbl, key, err)
			}
		}:
		default:
		}
	}
}

func runReads(pools map[string]*keyPool, rate float64, stop <-chan struct{}) {
	if rate <= 0 {
		return
	}
	nw := workerCount(rate)
	jobs, wg := startWorkers(nw)

	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	tables := []string{usersTable, productsTable, ordersTable}
	idx := 0
	for {
		select {
		case <-stop:
			close(jobs)
			wg.Wait()
			return
		case <-ticker.C:
		}
		tbl := tables[idx%len(tables)]
		idx++

		select {
		case jobs <- func() {
			key, ok := pools[tbl].random()
			if !ok {
				return
			}
			ok, err := doGet(tbl, key)
			if ok {
				readCnt.Add(1)
			} else if err != nil {
				errCnt.Add(1)
				logErr("GET", tbl, key, err)
			}
		}:
		default:
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// logErr rate-limits error logging so we don't flood the log.
var logErrMu sync.Mutex
var logErrCount int

func logErr(op, table, key string, err error) {
	logErrMu.Lock()
	logErrCount++
	n := logErrCount
	logErrMu.Unlock()
	if n <= 5 || n%100 == 0 {
		log.Printf("error: %s %s/%s: %v", op, table, key, err)
	}
}

// getClusterURLs returns all node URLs from cluster status so traffic can round-robin across the cluster.
func getClusterURLs(base string) []string {
	req, err := http.NewRequest("GET", base+"/api/v1/cluster/status", nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	var status struct {
		Total int `json:"total_nodes"`
		Nodes []struct {
			Addr string `json:"addr"`
		} `json:"nodes"`
	}
	if json.NewDecoder(resp.Body).Decode(&status) != nil || status.Total < 1 {
		resp.Body.Close()
		return nil
	}
	resp.Body.Close()
	var list []string
	seen := make(map[string]bool)
	for _, n := range status.Nodes {
		base := strings.TrimSpace(n.Addr)
		if base == "" {
			continue
		}
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			base = "http://" + base
		}
		base = strings.TrimSuffix(base, "/")
		if seen[base] {
			continue
		}
		seen[base] = true
		list = append(list, base)
	}
	return list
}

func ensureTables(tables []string) error {
	// Create tables on every URL we know about (urlList)
	for _, base := range urlList {
		if err := createTablesOn(base, tables); err != nil {
			return err
		}
	}
	// When using a single node, fetch cluster and create tables on other nodes so node 2 and 3 have tables
	if len(urlList) == 1 {
		var status struct {
			Total int `json:"total_nodes"`
			Nodes []struct {
				Addr string `json:"addr"`
			} `json:"nodes"`
		}
		req, err := http.NewRequest("GET", urlList[0]+"/api/v1/cluster/status", nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				if json.NewDecoder(resp.Body).Decode(&status) == nil && status.Total > 1 {
					resp.Body.Close()
					seen := make(map[string]bool)
					for _, u := range urlList {
						seen[strings.TrimSuffix(u, "/")] = true
					}
					for _, n := range status.Nodes {
						base := strings.TrimSpace(n.Addr)
						if base == "" {
							continue
						}
						if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
							base = "http://" + base
						}
						base = strings.TrimSuffix(base, "/")
						if seen[base] {
							continue
						}
						seen[base] = true
						if err := createTablesOn(base, tables); err != nil {
							log.Printf("Warning: create tables on %s: %v", base, err)
						}
					}
				} else {
					resp.Body.Close()
				}
			} else if resp != nil {
				resp.Body.Close()
			}
		}
	}
	return nil
}

func createTablesOn(base string, tables []string) error {
	for _, name := range tables {
		body, _ := json.Marshal(map[string]string{"name": name})
		req, err := http.NewRequest("POST", base+"/api/v1/tables", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("create table %q on %s: %w", name, base, err)
		}
		drainClose(resp)
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
			return fmt.Errorf("create table %q on %s: server returned %d", name, base, resp.StatusCode)
		}
	}
	return nil
}

// drainClose fully reads and closes a response body so the underlying
// TCP connection can be returned to the pool for reuse.
func drainClose(resp *http.Response) {
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func doPut(table, key string, body []byte) (bool, error) {
	_, ok, err := doPutWithConsistency(table, key, body, "")
	return ok, err
}

// doPutWithConsistency performs PUT with optional ?consistency=strong|eventual and returns latency, success, and error.
func doPutWithConsistency(table, key string, body []byte, consistency string) (latency time.Duration, ok bool, err error) {
	path := "/api/v1/tables/" + table + "/items/" + url.PathEscape(key)
	if consistency == "strong" {
		path += "?consistency=strong"
	} else if consistency == "eventual" {
		path += "?consistency=eventual"
	}
	req, err := http.NewRequest("PUT", getBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	latency = time.Since(start)
	if err != nil {
		return latency, false, err
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusOK {
		return latency, true, nil
	}
	bodyRead, _ := io.ReadAll(resp.Body)
	return latency, false, fmt.Errorf("%d %s", resp.StatusCode, string(bodyRead))
}

func doGet(table, key string) (bool, error) {
	path := "/api/v1/tables/" + table + "/items/" + url.PathEscape(key)
	req, err := http.NewRequest("GET", getBaseURL()+path, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil // key was deleted – not an error
	}
	bodyRead, _ := io.ReadAll(resp.Body)
	return false, fmt.Errorf("%d %s", resp.StatusCode, string(bodyRead))
}

func doDelete(table, key string) (bool, error) {
	path := "/api/v1/tables/" + table + "/items/" + url.PathEscape(key)
	req, err := http.NewRequest("DELETE", getBaseURL()+path, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	bodyRead, _ := io.ReadAll(resp.Body)
	return false, fmt.Errorf("%d %s", resp.StatusCode, string(bodyRead))
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func runStats(stop <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		i := insertCnt.Load()
		u := updateCnt.Load()
		d := deleteCnt.Load()
		r := readCnt.Load()
		e := errCnt.Load()
		log.Printf("stats | inserts=%d updates=%d deletes=%d reads=%d errors=%d", i, u, d, r, e)
	}
}

// ---------------------------------------------------------------------------
// Stress test mode
// ---------------------------------------------------------------------------

type stressResult struct {
	step   int
	rate   float64
	total  int64
	errors int64
	errPct float64
	passed bool
}

func runStress(pools map[string]*keyPool, mu *sync.Mutex, counters map[string]int, sig <-chan os.Signal) {
	// Compute base rates and ratios for proportional scaling
	baseInserts := insertsPS
	baseUpdates := updatesPS
	baseDeletes := deletesPS
	baseReads := readsPS
	baseTotal := baseInserts + baseUpdates + baseDeletes + baseReads

	// Sensible defaults if everything is zero
	if baseTotal == 0 {
		baseInserts, baseUpdates, baseDeletes, baseReads = 50, 20, 5, 10
		baseTotal = 85
	}

	insertRatio := baseInserts / baseTotal
	updateRatio := baseUpdates / baseTotal
	deleteRatio := baseDeletes / baseTotal
	readRatio := baseReads / baseTotal

	currentRate := baseTotal
	var results []stressResult

	for step := 1; ; step++ {
		curInserts := currentRate * insertRatio
		curUpdates := currentRate * updateRatio
		curDeletes := currentRate * deleteRatio
		curReads := currentRate * readRatio

		log.Printf("Step %d: %.0f req/s (inserts=%.1f updates=%.1f deletes=%.1f reads=%.1f)",
			step, currentRate, curInserts, curUpdates, curDeletes, curReads)

		// Reset counters
		insertCnt.Store(0)
		updateCnt.Store(0)
		deleteCnt.Store(0)
		readCnt.Store(0)
		errCnt.Store(0)
		logErrMu.Lock()
		logErrCount = 0
		logErrMu.Unlock()

		stop := make(chan struct{})
		var stepWg sync.WaitGroup

		if curInserts > 0 {
			stepWg.Add(1)
			go func() { defer stepWg.Done(); runInserts(pools, mu, counters, curInserts, stop) }()
		}
		if curUpdates > 0 {
			stepWg.Add(1)
			go func() { defer stepWg.Done(); runUpdates(pools, curUpdates, stop) }()
		}
		if curDeletes > 0 {
			stepWg.Add(1)
			go func() { defer stepWg.Done(); runDeletes(pools, curDeletes, stop) }()
		}
		if curReads > 0 {
			stepWg.Add(1)
			go func() { defer stepWg.Done(); runReads(pools, curReads, stop) }()
		}

		// Wait for step duration or user interrupt
		interrupted := false
		select {
		case <-time.After(stressDuration):
		case <-sig:
			interrupted = true
		}

		close(stop)
		stepWg.Wait() // wait for all workers to finish in-flight requests

		totalOps := insertCnt.Load() + updateCnt.Load() + deleteCnt.Load() + readCnt.Load() + errCnt.Load()
		errors := errCnt.Load()
		errPct := 0.0
		if totalOps > 0 {
			errPct = float64(errors) / float64(totalOps) * 100
		}
		passed := errPct < stressErrorThreshold*100

		results = append(results, stressResult{
			step:   step,
			rate:   currentRate,
			total:  totalOps,
			errors: errors,
			errPct: errPct,
			passed: passed,
		})

		if interrupted || !passed {
			printStressResults(results)
			return
		}

		// Ramp up
		currentRate += stressStep
	}
}

func printStressResults(results []stressResult) {
	fmt.Println()
	fmt.Println("Stress test results:")
	maxClean := 0.0
	for _, r := range results {
		status := "OK"
		if !r.passed {
			status = "FAIL"
		} else {
			maxClean = r.rate
		}
		fmt.Printf("  Step %d:  %6.0f req/s  errors=%-6d (%.1f%%)   %s\n",
			r.step, r.rate, r.errors, r.errPct, status)
	}
	fmt.Println("  ─────────────────────────────────────────────")
	fmt.Printf("  Max sustainable rate: %.0f req/s\n", maxClean)
	fmt.Println()
}

// ---------------------------------------------------------------------------
// Write-stress: ramp strong writes until p95 latency hits limit → benchmark
// ---------------------------------------------------------------------------

const (
	latencyBufferMaxSamples = 100_000
	minSamplesForP95        = 50
)

type latencyBuffer struct {
	mu sync.Mutex
	d  []time.Duration
}

func (b *latencyBuffer) Add(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.d) < latencyBufferMaxSamples {
		b.d = append(b.d, d)
	} else {
		// drop oldest (circular would be better; overwrite for simplicity)
		b.d = append(b.d[1:], d)
	}
}

// P95 returns the 95th percentile latency in milliseconds. Returns 0 if too few samples.
func (b *latencyBuffer) P95() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.d)
	if n < minSamplesForP95 {
		return 0
	}
	// copy and sort to compute percentile
	sorted := make([]time.Duration, n)
	copy(sorted, b.d)
	// simple sort (we only need element at 95th percentile index)
	sortDurations(sorted)
	idx := int(math.Ceil(0.95*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	return float64(sorted[idx].Milliseconds())
}

func sortDurations(d []time.Duration) {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
}

func (b *latencyBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.d = b.d[:0]
}

func (b *latencyBuffer) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.d)
}

func runWriteStress(pools map[string]*keyPool, mu *sync.Mutex, counters map[string]int, sig <-chan os.Signal) {
	if writeStressSingleNode {
		forceSingleURL.Store(true)
	}
	cons := writeStressConsistency
	if cons != "strong" && cons != "eventual" {
		cons = "strong"
	}

	var buf latencyBuffer
	currentRate := writeStressInitial
	prevRate := currentRate
	tables := []string{usersTable, productsTable, ordersTable}
	step := 0

	for {
		step++
		buf.Reset()
		insertCnt.Store(0)
		errCnt.Store(0)

		log.Printf("Write-stress step %d: %.0f %s writes/s (p95 limit %.0f ms)", step, currentRate, cons, writeStressP95LimitMs)

		stop := make(chan struct{})
		nw := workerCount(currentRate)
		jobs, wg := startWorkers(nw)
		interval := time.Duration(float64(time.Second) / currentRate)
		ticker := time.NewTicker(interval)
		idx := 0

		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					close(jobs)
					return
				case <-ticker.C:
				}
				table := tables[idx%len(tables)]
				idx++
				mu.Lock()
				counters[table]++
				keyNum := counters[table]
				mu.Unlock()
				tbl, kn := table, keyNum
				select {
				case jobs <- func() {
					var key string
					var body []byte
					switch tbl {
					case usersTable:
						key = fmt.Sprintf("user-%d", kn)
						body = mustJSON(map[string]interface{}{
							"name":  fmt.Sprintf("User %d", kn),
							"email": fmt.Sprintf("user%d@example.com", kn),
							"age":   rand.Intn(40) + 20,
						})
					case productsTable:
						key = fmt.Sprintf("prod-%d", kn)
						body = mustJSON(map[string]interface{}{
							"name":     productNames[rand.Intn(len(productNames))],
							"price":    float64(rand.Intn(900)+100) + 0.99,
							"in_stock": true,
						})
					case ordersTable:
						key = fmt.Sprintf("order-%d", kn)
						body = mustJSON(map[string]interface{}{
							"user_id":    fmt.Sprintf("user-%03d", rand.Intn(20)+1),
							"product_id": fmt.Sprintf("prod-%03d", rand.Intn(10)+1),
							"quantity":   rand.Intn(5) + 1,
							"status":     "completed",
						})
					}
					latency, ok, err := doPutWithConsistency(tbl, key, body, cons)
					buf.Add(latency)
					if ok {
						insertCnt.Add(1)
						pools[tbl].add(key)
					} else {
						errCnt.Add(1)
						if err != nil {
							logErr("PUT", tbl, key, err)
						}
					}
				}:
				default:
				}
			}
		}()

		select {
		case <-time.After(writeStressDuration):
		case <-sig:
			log.Println("Write-stress interrupted")
			ticker.Stop()
			close(stop)
			wg.Wait()
			printWriteStressBenchmark(prevRate, step-1)
			return
		}

		close(stop)
		wg.Wait()

		n := buf.Count()
		p95 := buf.P95()
		writes := insertCnt.Load()
		errors := errCnt.Load()

		log.Printf("Write-stress step %d done: %.0f writes/s  samples=%d  p95=%.0f ms  ok=%d errors=%d",
			step, currentRate, n, p95, writes, errors)

		if n < minSamplesForP95 {
			log.Printf("Write-stress: too few samples (%d), stopping", n)
			printWriteStressBenchmark(prevRate, step)
			return
		}

		if p95 >= writeStressP95LimitMs {
			log.Printf("Write-stress: p95 %.0f ms >= limit %.0f ms → stopping", p95, writeStressP95LimitMs)
			printWriteStressBenchmark(prevRate, step)
			return
		}

		prevRate = currentRate
		currentRate += writeStressStep
	}
}

func printWriteStressBenchmark(benchmarkRate float64, steps int) {
	cons := writeStressConsistency
	if cons != "strong" && cons != "eventual" {
		cons = "strong"
	}
	fmt.Println()
	fmt.Printf("Write-stress benchmark (%s writes):\n", cons)
	fmt.Printf("  Steps completed: %d\n", steps)
	fmt.Printf("  Max write rate before p95 exceeded limit: %.0f %s writes/s\n", benchmarkRate, cons)
	fmt.Println()
}
