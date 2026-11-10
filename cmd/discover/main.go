package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/charliewilco/keystoner/internal/discovery"
)

func run() error {
	configPath := flag.String("config", "discovery/sources.json", "Source allowlist and start URLs")
	seed := flag.String("seed", "seed/catalog.sql", "Trusted catalog SQL")
	state := flag.String("state", "work/discovery/state.sqlite", "Separate crawl baseline database")
	output := flag.String("output", "work/discovery/report.json", "Review report path")
	maxPages := flag.Int("max-pages", 30, "Maximum pages to attempt (1–500)")
	depth := flag.Int("depth", 1, "Maximum link depth (0–2)")
	flag.Parse()
	data, e := os.ReadFile(*configPath)
	if e != nil {
		return e
	}
	var config discovery.Config
	if e = json.Unmarshal(data, &config); e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, e := discovery.Crawl(ctx, config, *seed, *state, *maxPages, *depth, nil)
	if e != nil {
		return e
	}
	data, e = json.MarshalIndent(report, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(*output), 0755); e != nil {
		return e
	}
	if e = os.WriteFile(*output, append(data, '\n'), 0644); e != nil {
		return e
	}
	fmt.Printf("pages=%d candidates=%d errors=%d\n", len(report.Pages), len(report.Candidates), len(report.Errors))
	if len(report.Pages) == 0 {
		return fmt.Errorf("no pages successfully fetched; inspect report errors")
	}
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
