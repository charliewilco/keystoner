// Package cli adapts command-line inputs and output files to Keystoner tools.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/charliewilco/keystoner/core"
	"github.com/charliewilco/keystoner/tools/scraper"
	"io"
	"os"
	"path/filepath"
)

// Discover runs source discovery without owning process signals or exiting.
func Discover(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("discover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "discovery/sources.json", "Source allowlist and start URLs")
	seed := flags.String("seed", "seed/catalog.sql", "Trusted catalog SQL")
	state := flags.String("state", "work/discovery/state.sqlite", "Separate crawl baseline database")
	output := flags.String("output", "work/discovery/report.json", "Review report path")
	maxPages := flags.Int("max-pages", 30, "Maximum pages to attempt (1–500)")
	depth := flags.Int("depth", 1, "Maximum link depth (0–2)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	data, e := os.ReadFile(*configPath)
	if e != nil {
		return e
	}
	var config scraper.Config
	if e = json.Unmarshal(data, &config); e != nil {
		return e
	}
	sources, e := core.ReadSeedSources(*seed)
	if e != nil {
		return e
	}
	report, e := scraper.Crawl(ctx, config, sources, *state, *maxPages, *depth, nil)
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
	fmt.Fprintf(stdout, "pages=%d candidates=%d errors=%d\n", len(report.Pages), len(report.Candidates), len(report.Errors))
	if len(report.Pages) == 0 {
		return fmt.Errorf("no pages successfully fetched; inspect report errors")
	}
	return nil
}
