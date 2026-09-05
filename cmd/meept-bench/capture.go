package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bhodgens/meept-bench/internal/fixtures"
)

// captureCmd implements `meept-bench capture --url URL --out DIR`: fetch
// the URL live ONCE and write <dir>/<sha1(url)>.json in the shared
// {url, fetched_at, body} fixture format so deterministic suites
// (tools: {cached_fetch: true}) can serve it from cache with networking
// disabled. The same fixture file is what meept's cached-fetch lookup
// reads from ~/.meept/tool-cache or $MEEPT_TOOL_CACHE_DIR.
func captureCmd(args []string) {
	fs := flag.NewFlagSet("capture", flag.ExitOnError)
	urls := fs.String("url", "", "URL to capture (comma-separated list accepted)")
	out := fs.String("out", "fixtures", "fixture output directory (gitignored)")
	timeout := fs.Int("timeout", 30, "per-URL fetch timeout in seconds")
	force := fs.Bool("force", false, "re-capture even when a fixture already exists")
	fs.Parse(args)

	if *urls == "" {
		fmt.Fprintln(os.Stderr, "capture: --url is required")
		os.Exit(2)
	}
	store := fixtures.New(*out)

	var failed bool
	for _, raw := range strings.Split(*urls, ",") {
		url := strings.TrimSpace(raw)
		if url == "" {
			continue
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			fmt.Fprintf(os.Stderr, "capture: %s: only http(s) URLs are supported\n", url)
			failed = true
			continue
		}
		if already, err := store.Has(url); err == nil && already && !*force {
			fmt.Printf("capture: %s already captured -> %s (use --force to re-capture)\n",
				url, filepath.Join(*out, fixtures.HashURL(url)+".json"))
			continue
		}
		body, err := fetchOnce(context.Background(), url, time.Duration(*timeout)*time.Second)
		if err != nil {
			fmt.Fprintf(os.Stderr, "capture: %s: %v\n", url, err)
			failed = true
			continue
		}
		path, err := store.Save(url, body, time.Now())
		if err != nil {
			fmt.Fprintf(os.Stderr, "capture: %s: %v\n", url, err)
			failed = true
			continue
		}
		fmt.Printf("capture: %s -> %s (%d bytes)\n", url, path, len(body))
	}
	if failed {
		os.Exit(1)
	}
}

// fetchOnce performs exactly one live GET of url and returns the body
// (capped like meept's web_fetch at 100 KiB).
func fetchOnce(ctx context.Context, url string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "meept-bench-capture/0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
