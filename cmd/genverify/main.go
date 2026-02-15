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
	"sync/atomic"
	"syscall"
	"time"
)

var (
	baseURL      string
	urls         string
	urlList      []string
	strong       bool
	rate         float64
	iterations   int
	table        string
	passCount    atomic.Int64
	failCount    atomic.Int64
	errorCount   atomic.Int64
)

var client = &http.Client{Timeout: 10 * time.Second}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `genverify - Insert on one node, read immediately on another to verify consistency.

Usage: genverify [flags]

Per iteration: picks a random write node, inserts a row, reads from a different
node immediately. Reports pass/fail. Use -strong to test strong consistency
(quorum reads/writes); without it uses eventual consistency.

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Example:
  ./bin/genverify
  ./bin/genverify -strong
  ./bin/genverify -urls http://127.0.0.1:8081,http://127.0.0.1:8082,http://127.0.0.1:8083 -strong -rate 10
`)
	}

	flag.StringVar(&baseURL, "url", getEnv("HEDGEHOG_URL", "http://localhost:8081"), "base URL (used when -urls not set)")
	flag.StringVar(&urls, "urls", "", "comma-separated node URLs to use for write/read")
	flag.BoolVar(&strong, "strong", false, "use strong consistency (quorum read/write)")
	flag.Float64Var(&rate, "rate", 5, "iterations per second")
	flag.IntVar(&iterations, "n", 0, "max iterations (0 = run until Ctrl+C)")
	flag.StringVar(&table, "table", "verify", "table name")
	flag.Parse()

	// Parse node list
	if urls != "" {
		for _, u := range strings.Split(urls, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				urlList = append(urlList, strings.TrimSuffix(u, "/"))
			}
		}
	}
	if len(urlList) == 0 {
		// Try to discover cluster from single baseURL
		if all := getClusterURLs(strings.TrimSuffix(baseURL, "/")); len(all) > 1 {
			urlList = all
		} else if baseURL != "" {
			urlList = []string{strings.TrimSuffix(baseURL, "/")}
		}
	}
	if len(urlList) < 2 {
		log.Fatalf("Need at least 2 nodes for cross-node verify. Got: %v. Use -urls http://...:8081,http://...:8082,...", urlList)
	}

	consistency := "eventual"
	if strong {
		consistency = "strong"
	}
	log.Printf("genverify | table=%s consistency=%s rate=%.1f/s nodes=%d", table, consistency, rate, len(urlList))

	if err := ensureTable(); err != nil {
		log.Fatalf("Cannot create table: %v", err)
	}

	ctx := make(chan os.Signal, 1)
	signal.Notify(ctx, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan struct{})
	go runVerify(consistency, done)
	go runStats()

	select {
	case <-ctx:
		log.Println("Stopping...")
	case <-done:
		// Finished -n iterations; exit with stats
	}
	p, f, e := passCount.Load(), failCount.Load(), errorCount.Load()
	total := p + f + e
	passRate := 0.0
	if total > 0 {
		passRate = 100 * float64(p) / float64(total)
	}
	log.Printf("Final: pass=%d fail=%d errors=%d (%.1f%% pass)", p, f, e, passRate)
}

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
		addr := strings.TrimSpace(n.Addr)
		if addr == "" {
			continue
		}
		if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
			addr = "http://" + addr
		}
		addr = strings.TrimSuffix(addr, "/")
		if seen[addr] {
			continue
		}
		seen[addr] = true
		list = append(list, addr)
	}
	return list
}

func ensureTable() error {
	body, _ := json.Marshal(map[string]string{"name": table})
	for _, base := range urlList {
		req, err := http.NewRequest("POST", base+"/api/v1/tables", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("create table on %s: %w", base, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
			b, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("create table on %s: %d %s", base, resp.StatusCode, string(b))
		}
	}
	return nil
}

func runVerify(consistency string, done chan struct{}) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer func() {
		if iterations > 0 {
			close(done)
		}
	}()

	n := 0
	for range ticker.C {
		if iterations > 0 && n >= iterations {
			return
		}
		n++

		writeIdx := rng.Intn(len(urlList))
		readIdx := rng.Intn(len(urlList))
		for readIdx == writeIdx {
			readIdx = rng.Intn(len(urlList))
		}

		writeURL := urlList[writeIdx]
		readURL := urlList[readIdx]

		key := fmt.Sprintf("verify-%d-%d", time.Now().UnixNano(), rng.Int63())
		doc := map[string]interface{}{
			"ts":       time.Now().UnixNano(),
			"write_at": writeURL,
			"rand":     rng.Int63(),
		}
		body, _ := json.Marshal(doc)

		// PUT to write node
		putPath := "/api/v1/tables/" + table + "/items/" + url.PathEscape(key)
		if consistency == "strong" {
			putPath += "?consistency=strong"
		}
		putReq, err := http.NewRequest("PUT", writeURL+putPath, bytes.NewReader(body))
		if err != nil {
			errorCount.Add(1)
			continue
		}
		putReq.Header.Set("Content-Type", "application/json")
		putResp, err := client.Do(putReq)
		if err != nil {
			errorCount.Add(1)
			continue
		}
		putResp.Body.Close()
		if putResp.StatusCode != http.StatusOK {
			errorCount.Add(1)
			continue
		}

		// GET from read node (different node)
		getPath := "/api/v1/tables/" + table + "/items/" + url.PathEscape(key)
		if consistency == "strong" {
			getPath += "?consistency=strong"
		}
		getReq, err := http.NewRequest("GET", readURL+getPath, nil)
		if err != nil {
			errorCount.Add(1)
			continue
		}
		getResp, err := client.Do(getReq)
		if err != nil {
			errorCount.Add(1)
			continue
		}
		getBody, _ := io.ReadAll(getResp.Body)
		getResp.Body.Close()

		if getResp.StatusCode == http.StatusNotFound {
			failCount.Add(1)
			continue
		}
		if getResp.StatusCode != http.StatusOK {
			errorCount.Add(1)
			continue
		}

		var result struct {
			Key  string                 `json:"key"`
			Item map[string]interface{} `json:"item"`
		}
		if err := json.Unmarshal(getBody, &result); err != nil {
			errorCount.Add(1)
			continue
		}

		// Compare docs (normalize via map to handle int/float JSON round-trip)
		var want map[string]interface{}
		if err := json.Unmarshal(body, &want); err != nil {
			errorCount.Add(1)
			continue
		}
		if !docsEqual(want, result.Item) {
			failCount.Add(1)
			continue
		}

		passCount.Add(1)
	}
}

func docsEqual(a, b map[string]interface{}) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

func runStats() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		p := passCount.Load()
		f := failCount.Load()
		e := errorCount.Load()
		total := p + f + e
		passRate := float64(0)
		if total > 0 {
			passRate = 100 * float64(p) / float64(total)
		}
		log.Printf("stats | pass=%d fail=%d errors=%d (%.1f%% pass)", p, f, e, passRate)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
