package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bhodgens/meept-bench/internal/daemonclient"
)

func main() {
	c := daemonclient.New("/Users/caimlas/.meept/meept.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out map[string]any
	err := c.Call(ctx, "memory.query", map[string]any{
		"query": "deployment codeword BENCH-7391",
		"limit": 5,
	}, &out)
	fmt.Println("memory.query err:", err)
	if items, ok := out["results"].([]any); ok {
		for _, it := range items {
			m, _ := it.(map[string]any)
			content, _ := m["content"].(string)
			if len(content) > 90 {
				content = content[:90]
			}
			fmt.Printf("rel=%v | %s\n", m["relevance_score"], content)
		}
	} else {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b)[:2000])
	}
}
