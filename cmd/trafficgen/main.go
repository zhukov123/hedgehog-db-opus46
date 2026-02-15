package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	baseURL   string
	urls      string // comma-separated; if set we round-robin across these instead of single baseURL
	urlList   []string
	urlIndex  atomic.Uint64
	insertsPS float64
	updatesPS float64
	deletesPS float64
)

var (
	client    = &http.Client{Timeout: 10 * time.Second}
	insertCnt atomic.Int64
	updateCnt atomic.Int64
	deleteCnt atomic.Int64
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
		fmt.Fprintf(os.Stderr, `trafficgen - Generate live insert/update/delete traffic for HedgehogDB demo tables (users, products, orders).

Usage: trafficgen [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Environment:
  HEDGEHOG_URL  Base URL if -url is not set (default: http://localhost:8081, first cluster node)

Example:
  ./bin/trafficgen
  ./bin/trafficgen -inserts 10 -updates 3 -deletes 1
  ./bin/trafficgen -urls http://127.0.0.1:8081,http://127.0.0.1:8082,http://127.0.0.1:8083
`)
	}
	flag.StringVar(&baseURL, "url", getEnv("HEDGEHOG_URL", "http://localhost:8081"), "HedgehogDB base URL (used when -urls is not set; default 8081 = first cluster node)")
	flag.StringVar(&urls, "urls", "", "comma-separated node URLs to round-robin across (overrides -url)")
	flag.Float64Var(&insertsPS, "inserts", 5, "inserts per second (across all tables; keep > updates+deletes so data grows)")
	flag.Float64Var(&updatesPS, "updates", 2, "updates per second (across all tables)")
	flag.Float64Var(&deletesPS, "deletes", 1, "deletes per second (across all tables)")
	flag.Parse()

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

	log.Printf("Traffic gen | inserts=%.1f/s updates=%.1f/s deletes=%.1f/s (data grows: inserts > updates+deletes)", insertsPS, updatesPS, deletesPS)
	logNodeTarget()

	tables := []string{usersTable, productsTable, ordersTable}

	// Ensure demo tables exist (idempotent; 409 = already exists)
	if err := ensureTables(tables); err != nil {
		log.Fatalf("Cannot reach server or create tables: %v\nTip: Cluster runs on 8081-8083. Try: ./bin/trafficgen -url http://localhost:8081", err)
	}

	// When we had a single URL, send traffic to all cluster nodes so counts grow on every node
	if len(urlList) == 1 {
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
			log.Printf("Warning: scan %s: %v (updates/deletes will only use keys created by this run)", name, err)
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

	ctx := make(chan os.Signal, 1)
	signal.Notify(ctx, syscall.SIGINT, syscall.SIGTERM)

	if insertsPS > 0 {
		go runInserts(pools, &insertCountersMu, insertCounters, insertsPS)
	}
	if updatesPS > 0 {
		go runUpdates(pools, updatesPS)
	}
	if deletesPS > 0 {
		go runDeletes(pools, deletesPS)
	}
	go runStats()

	<-ctx
	log.Println("Stopping...")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getBaseURL returns the next URL when round-robin across urlList; otherwise the single URL.
func getBaseURL() string {
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
					NodeID  string `json:"node_id"`
					BindAddr string `json:"bind_addr"`
					Total   int    `json:"total_nodes"`
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

func runInserts(pools map[string]*keyPool, mu *sync.Mutex, counters map[string]int, rate float64) {
	if rate <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	tables := []string{usersTable, productsTable, ordersTable}
	idx := 0
	for range ticker.C {
		table := tables[idx%len(tables)]
		idx++

		mu.Lock()
		counters[table]++
		keyNum := counters[table]
		mu.Unlock()

		var key string
		var body []byte
		switch table {
		case usersTable:
			key = fmt.Sprintf("user-%d", keyNum)
			body = mustJSON(map[string]interface{}{
				"name":  fmt.Sprintf("User %d", keyNum),
				"email": fmt.Sprintf("user%d@example.com", keyNum),
				"age":   rand.Intn(40) + 20,
			})
		case productsTable:
			key = fmt.Sprintf("prod-%d", keyNum)
			body = mustJSON(map[string]interface{}{
				"name":     productNames[rand.Intn(len(productNames))],
				"price":    float64(rand.Intn(900)+100) + 0.99,
				"in_stock": true,
			})
		case ordersTable:
			key = fmt.Sprintf("order-%d", keyNum)
			body = mustJSON(map[string]interface{}{
				"user_id":    fmt.Sprintf("user-%03d", rand.Intn(20)+1),
				"product_id": fmt.Sprintf("prod-%03d", rand.Intn(10)+1),
				"quantity":   rand.Intn(5) + 1,
				"status":     "completed",
			})
		}

		ok, err := doPut(table, key, body)
		if ok {
			insertCnt.Add(1)
			pools[table].add(key)
		} else {
			errCnt.Add(1)
			logErr("PUT", table, key, err)
		}
	}
}

func runUpdates(pools map[string]*keyPool, rate float64) {
	if rate <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	tables := []string{usersTable, productsTable, ordersTable}
	idx := 0
	for range ticker.C {
		table := tables[idx%len(tables)]
		idx++

		key, ok := pools[table].random()
		if !ok {
			continue
		}

		var body []byte
		switch table {
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

		ok, err := doPut(table, key, body)
		if ok {
			updateCnt.Add(1)
		} else {
			errCnt.Add(1)
			logErr("PUT", table, key, err)
		}
	}
}

func runDeletes(pools map[string]*keyPool, rate float64) {
	if rate <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	tables := []string{usersTable, productsTable, ordersTable}
	idx := 0
	for range ticker.C {
		table := tables[idx%len(tables)]
		idx++

		key, ok := pools[table].takeRandom()
		if !ok {
			continue
		}

		ok, err := doDelete(table, key)
		if ok {
			deleteCnt.Add(1)
		} else {
			errCnt.Add(1)
			logErr("DELETE", table, key, err)
		}
	}
}

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
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
			return fmt.Errorf("create table %q on %s: server returned %d", name, base, resp.StatusCode)
		}
	}
	return nil
}

func doPut(table, key string, body []byte) (bool, error) {
	path := "/api/v1/tables/" + table + "/items/" + url.PathEscape(key)
	req, err := http.NewRequest("PUT", getBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true, nil
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
	defer resp.Body.Close()
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

func runStats() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		i := insertCnt.Load()
		u := updateCnt.Load()
		d := deleteCnt.Load()
		e := errCnt.Load()
		log.Printf("stats | inserts=%d updates=%d deletes=%d errors=%d", i, u, d, e)
	}
}
