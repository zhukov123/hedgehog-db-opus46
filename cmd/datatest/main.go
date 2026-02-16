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
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var client = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        300,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	},
}

func main() {
	var (
		baseURL    string
		urls       string
		tableName  string
		totalKeys  int
		deleteKeys int
		updateKeys int
		workers    int
	)

	flag.StringVar(&baseURL, "url", "http://localhost:8081", "HedgehogDB base URL")
	flag.StringVar(&urls, "urls", "", "comma-separated node URLs to round-robin")
	flag.StringVar(&tableName, "table", "datatest", "table name to use")
	flag.IntVar(&totalKeys, "keys", 1000000, "number of keys to insert")
	flag.IntVar(&deleteKeys, "deletes", 100000, "number of keys to delete and verify gone")
	flag.IntVar(&updateKeys, "updates", 100000, "number of keys to update and verify")
	flag.IntVar(&workers, "workers", 64, "concurrent workers")
	flag.Parse()

	var urlList []string
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

	// Auto-discover cluster nodes
	if len(urlList) == 1 {
		if all := discoverNodes(urlList[0]); len(all) > 1 {
			urlList = all
			log.Printf("Discovered %d nodes, round-robin across all", len(urlList))
		}
	}

	var urlIdx atomic.Uint64
	getURL := func() string {
		i := urlIdx.Add(1) % uint64(len(urlList))
		return urlList[i]
	}

	log.Printf("datatest | table=%s keys=%d deletes=%d updates=%d workers=%d nodes=%d",
		tableName, totalKeys, deleteKeys, updateKeys, workers, len(urlList))

	// Ensure table exists on all nodes
	for _, u := range urlList {
		ensureTable(u, tableName)
	}

	// =========================================================================
	// Phase 1: Insert all keys
	// =========================================================================
	log.Printf("Phase 1: Inserting %d keys...", totalKeys)
	var insertOK, insertErr atomic.Int64
	start := time.Now()

	runParallel(workers, totalKeys, func(i int) {
		key := fmt.Sprintf("dt-%07d", i)
		doc := map[string]interface{}{
			"seq":   i,
			"value": fmt.Sprintf("original-%d", i),
			"tag":   "inserted",
		}
		if doPut(getURL(), tableName, key, doc) {
			insertOK.Add(1)
		} else {
			insertErr.Add(1)
		}
	})

	elapsed := time.Since(start)
	log.Printf("Phase 1 done: %d OK, %d errors (%.1f sec, %.0f ops/s)",
		insertOK.Load(), insertErr.Load(), elapsed.Seconds(),
		float64(insertOK.Load())/elapsed.Seconds())

	if insertErr.Load() > 0 {
		log.Printf("WARNING: %d insert errors, verification may show missing keys", insertErr.Load())
	}

	// =========================================================================
	// Phase 2: Verify all keys exist
	// =========================================================================
	log.Printf("Phase 2: Verifying %d keys exist...", totalKeys)
	var verifyOK, verifyMissing, verifyErr atomic.Int64
	start = time.Now()

	runParallel(workers, totalKeys, func(i int) {
		key := fmt.Sprintf("dt-%07d", i)
		status, _ := doGet(getURL(), tableName, key)
		switch status {
		case 200:
			verifyOK.Add(1)
		case 404:
			verifyMissing.Add(1)
		default:
			verifyErr.Add(1)
		}
	})

	elapsed = time.Since(start)
	log.Printf("Phase 2 done: %d found, %d missing, %d errors (%.1f sec, %.0f ops/s)",
		verifyOK.Load(), verifyMissing.Load(), verifyErr.Load(),
		elapsed.Seconds(), float64(totalKeys)/elapsed.Seconds())

	if verifyMissing.Load() > 0 || verifyErr.Load() > 0 {
		log.Printf("FAIL: Phase 2 — expected %d keys, found %d", totalKeys, verifyOK.Load())
	} else {
		log.Printf("PASS: Phase 2 — all %d keys verified", totalKeys)
	}

	// =========================================================================
	// Phase 3: Delete first N keys
	// =========================================================================
	if deleteKeys > totalKeys {
		deleteKeys = totalKeys
	}
	log.Printf("Phase 3: Deleting %d keys...", deleteKeys)
	var delOK, delErr atomic.Int64
	start = time.Now()

	runParallel(workers, deleteKeys, func(i int) {
		key := fmt.Sprintf("dt-%07d", i)
		if doDelete(getURL(), tableName, key) {
			delOK.Add(1)
		} else {
			delErr.Add(1)
		}
	})

	elapsed = time.Since(start)
	log.Printf("Phase 3 done: %d OK, %d errors (%.1f sec, %.0f ops/s)",
		delOK.Load(), delErr.Load(), elapsed.Seconds(),
		float64(delOK.Load())/elapsed.Seconds())

	// =========================================================================
	// Phase 4: Verify deleted keys are gone
	// =========================================================================
	log.Printf("Phase 4: Verifying %d deleted keys are gone...", deleteKeys)
	var goneOK, goneStillThere, goneErr atomic.Int64
	start = time.Now()

	runParallel(workers, deleteKeys, func(i int) {
		key := fmt.Sprintf("dt-%07d", i)
		status, _ := doGet(getURL(), tableName, key)
		switch status {
		case 404:
			goneOK.Add(1)
		case 200:
			goneStillThere.Add(1)
		default:
			goneErr.Add(1)
		}
	})

	elapsed = time.Since(start)
	log.Printf("Phase 4 done: %d gone, %d still exist, %d errors (%.1f sec)",
		goneOK.Load(), goneStillThere.Load(), goneErr.Load(), elapsed.Seconds())

	if goneStillThere.Load() > 0 || goneErr.Load() > 0 {
		log.Printf("FAIL: Phase 4 — %d keys still exist after delete", goneStillThere.Load())
	} else {
		log.Printf("PASS: Phase 4 — all %d deleted keys confirmed gone", deleteKeys)
	}

	// =========================================================================
	// Phase 5: Update N keys (from the surviving range)
	// =========================================================================
	surviving := totalKeys - deleteKeys
	if updateKeys > surviving {
		updateKeys = surviving
	}
	log.Printf("Phase 5: Updating %d keys...", updateKeys)
	var updOK, updErr atomic.Int64
	start = time.Now()

	// Update keys starting from deleteKeys (the first surviving key)
	runParallel(workers, updateKeys, func(i int) {
		keyIdx := deleteKeys + i
		key := fmt.Sprintf("dt-%07d", keyIdx)
		doc := map[string]interface{}{
			"seq":   keyIdx,
			"value": fmt.Sprintf("updated-%d", keyIdx),
			"tag":   "updated",
		}
		if doPut(getURL(), tableName, key, doc) {
			updOK.Add(1)
		} else {
			updErr.Add(1)
		}
	})

	elapsed = time.Since(start)
	log.Printf("Phase 5 done: %d OK, %d errors (%.1f sec, %.0f ops/s)",
		updOK.Load(), updErr.Load(), elapsed.Seconds(),
		float64(updOK.Load())/elapsed.Seconds())

	// =========================================================================
	// Phase 6: Verify updated keys have new values
	// =========================================================================
	log.Printf("Phase 6: Verifying %d updated keys...", updateKeys)
	var updVerifyOK, updVerifyWrong, updVerifyMissing, updVerifyErr atomic.Int64
	start = time.Now()

	runParallel(workers, updateKeys, func(i int) {
		keyIdx := deleteKeys + i
		key := fmt.Sprintf("dt-%07d", keyIdx)
		status, body := doGet(getURL(), tableName, key)
		switch status {
		case 200:
			var resp struct {
				Item map[string]interface{} `json:"item"`
			}
			if json.Unmarshal(body, &resp) == nil {
				tag, _ := resp.Item["tag"].(string)
				val, _ := resp.Item["value"].(string)
				expected := fmt.Sprintf("updated-%d", keyIdx)
				if tag == "updated" && val == expected {
					updVerifyOK.Add(1)
				} else {
					updVerifyWrong.Add(1)
				}
			} else {
				updVerifyErr.Add(1)
			}
		case 404:
			updVerifyMissing.Add(1)
		default:
			updVerifyErr.Add(1)
		}
	})

	elapsed = time.Since(start)
	log.Printf("Phase 6 done: %d correct, %d wrong value, %d missing, %d errors (%.1f sec)",
		updVerifyOK.Load(), updVerifyWrong.Load(), updVerifyMissing.Load(), updVerifyErr.Load(),
		elapsed.Seconds())

	if updVerifyWrong.Load() > 0 || updVerifyMissing.Load() > 0 || updVerifyErr.Load() > 0 {
		log.Printf("FAIL: Phase 6 — %d/%d keys have correct updated value",
			updVerifyOK.Load(), updateKeys)
	} else {
		log.Printf("PASS: Phase 6 — all %d updated keys verified", updateKeys)
	}

	// =========================================================================
	// Summary
	// =========================================================================
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Println("  DATA VALIDATION SUMMARY")
	fmt.Println("═══════════════════════════════════════════════════════")
	results := []struct {
		phase  string
		pass   bool
		detail string
	}{
		{"Insert 1M keys", insertErr.Load() == 0,
			fmt.Sprintf("%d OK, %d errors", insertOK.Load(), insertErr.Load())},
		{"Verify all exist", verifyMissing.Load() == 0 && verifyErr.Load() == 0,
			fmt.Sprintf("%d found, %d missing", verifyOK.Load(), verifyMissing.Load())},
		{"Delete 100K keys", delErr.Load() == 0,
			fmt.Sprintf("%d OK, %d errors", delOK.Load(), delErr.Load())},
		{"Verify deletes gone", goneStillThere.Load() == 0 && goneErr.Load() == 0,
			fmt.Sprintf("%d gone, %d still exist", goneOK.Load(), goneStillThere.Load())},
		{"Update 100K keys", updErr.Load() == 0,
			fmt.Sprintf("%d OK, %d errors", updOK.Load(), updErr.Load())},
		{"Verify updates", updVerifyWrong.Load() == 0 && updVerifyMissing.Load() == 0 && updVerifyErr.Load() == 0,
			fmt.Sprintf("%d correct, %d wrong, %d missing", updVerifyOK.Load(), updVerifyWrong.Load(), updVerifyMissing.Load())},
	}

	allPass := true
	for i, r := range results {
		status := "PASS"
		if !r.pass {
			status = "FAIL"
			allPass = false
		}
		fmt.Printf("  %d. %-22s  %s  (%s)\n", i+1, r.phase, status, r.detail)
	}
	fmt.Println("═══════════════════════════════════════════════════════")
	if allPass {
		fmt.Println("  RESULT: ALL PHASES PASSED")
	} else {
		fmt.Println("  RESULT: SOME PHASES FAILED")
		os.Exit(1)
	}
	fmt.Println()
}

// ---------------------------------------------------------------------------
// Parallel runner
// ---------------------------------------------------------------------------

func runParallel(workers, n int, fn func(i int)) {
	jobs := make(chan int, workers*2)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				fn(i)
			}
		}()
	}

	// Progress ticker
	var done atomic.Int64
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			d := done.Load()
			pct := float64(d) / float64(n) * 100
			log.Printf("  progress: %d/%d (%.1f%%)", d, n, pct)
		}
	}()

	for i := 0; i < n; i++ {
		jobs <- i
		done.Add(1)
	}
	close(jobs)
	wg.Wait()
	ticker.Stop()
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func doPut(base, table, key string, doc map[string]interface{}) bool {
	body, _ := json.Marshal(doc)
	path := "/api/v1/tables/" + table + "/items/" + url.PathEscape(key)
	req, _ := http.NewRequest("PUT", base+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode == 200
}

func doGet(base, table, key string) (int, []byte) {
	path := "/api/v1/tables/" + table + "/items/" + url.PathEscape(key)
	resp, err := client.Do(mustReq("GET", base+path))
	if err != nil {
		return 0, nil
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, body
}

func doDelete(base, table, key string) bool {
	path := "/api/v1/tables/" + table + "/items/" + url.PathEscape(key)
	resp, err := client.Do(mustReq("DELETE", base+path))
	if err != nil {
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode == 200
}

func mustReq(method, url string) *http.Request {
	req, _ := http.NewRequest(method, url, nil)
	return req
}

func ensureTable(base, name string) {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, _ := http.NewRequest("POST", base+"/api/v1/tables", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func discoverNodes(base string) []string {
	resp, err := client.Get(base + "/api/v1/cluster/status")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var status struct {
		Nodes []struct {
			Addr string `json:"addr"`
		} `json:"nodes"`
	}
	if json.NewDecoder(resp.Body).Decode(&status) != nil {
		return nil
	}
	var list []string
	for _, n := range status.Nodes {
		addr := strings.TrimSpace(n.Addr)
		if addr == "" {
			continue
		}
		if !strings.HasPrefix(addr, "http://") {
			addr = "http://" + addr
		}
		list = append(list, strings.TrimSuffix(addr, "/"))
	}
	return list
}

// suppress unused import warning
var _ = rand.Intn
