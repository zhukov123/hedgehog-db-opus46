package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 10 * time.Second}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	baseURL := os.Getenv("HEDGEHOG_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "create-table":
		err = createTable(baseURL, args)
	case "list-tables":
		err = listTables(baseURL)
	case "delete-table":
		err = deleteTable(baseURL, args)
	case "get":
		err = getItem(baseURL, args)
	case "put":
		err = putItem(baseURL, args)
	case "delete":
		err = deleteItem(baseURL, args)
	case "scan":
		err = scanItems(baseURL, args)
	case "cluster-status":
		err = clusterStatus(baseURL)
	case "cluster-nodes":
		err = clusterNodes(baseURL)
	case "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`hedgehogctl - HedgehogDB CLI

Usage: hedgehogctl <command> [args...]

Commands:
  create-table <name>                Create a new table
  list-tables                        List all tables
  delete-table <name>                Delete a table
  get <table> <key>                  Get an item
  put <table> <key> <json>           Put an item
  delete <table> <key>               Delete an item
  scan <table>                       Scan all items in a table
  cluster-status                     Show cluster status
  cluster-nodes                      List cluster nodes
  help                               Show this help

Environment:
  HEDGEHOG_URL  Base URL (default: http://localhost:8080)`)
}

func createTable(baseURL string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hedgehogctl create-table <name>")
	}
	body, _ := json.Marshal(map[string]string{"name": args[0]})
	return doRequest("POST", baseURL+"/api/v1/tables", body)
}

func listTables(baseURL string) error {
	return doRequest("GET", baseURL+"/api/v1/tables", nil)
}

func deleteTable(baseURL string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hedgehogctl delete-table <name>")
	}
	return doRequest("DELETE", baseURL+"/api/v1/tables/"+args[0], nil)
}

func getItem(baseURL string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: hedgehogctl get <table> <key>")
	}
	return doRequest("GET", baseURL+"/api/v1/tables/"+args[0]+"/items/"+args[1], nil)
}

func putItem(baseURL string, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: hedgehogctl put <table> <key> <json>")
	}
	jsonStr := strings.Join(args[2:], " ")
	return doRequest("PUT", baseURL+"/api/v1/tables/"+args[0]+"/items/"+args[1], []byte(jsonStr))
}

func deleteItem(baseURL string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: hedgehogctl delete <table> <key>")
	}
	return doRequest("DELETE", baseURL+"/api/v1/tables/"+args[0]+"/items/"+args[1], nil)
}

func scanItems(baseURL string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hedgehogctl scan <table>")
	}
	return doRequest("GET", baseURL+"/api/v1/tables/"+args[0]+"/items", nil)
}

func clusterStatus(baseURL string) error {
	return doRequest("GET", baseURL+"/api/v1/cluster/status", nil)
}

func clusterNodes(baseURL string) error {
	return doRequest("GET", baseURL+"/api/v1/cluster/nodes", nil)
}

func doRequest(method, url string, body []byte) error {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Pretty-print JSON
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, respBody, "", "  "); err == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(respBody))
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	return nil
}
