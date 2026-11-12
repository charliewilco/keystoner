package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/charliewilco/keystoner/tools/scraper"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverHelpAndInvalidFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		help bool
	}{{[]string{"--help"}, true}, {[]string{"--unknown"}, false}, {[]string{"unexpected"}, false}} {
		var stdout, stderr bytes.Buffer
		err := Discover(context.Background(), tc.args, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error or help")
		}
		if tc.help && (!errors.Is(err, flag.ErrHelp) || !strings.Contains(stderr.String(), "max-pages")) {
			t.Fatalf("help: %v %s", err, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatal("unexpected output")
		}
	}
}

func TestDiscoverInputFailuresDoNotWriteReport(t *testing.T) {
	for _, tc := range []struct {
		name, config, seed string
		extra              []string
	}{
		{"malformed config", "{", "", nil},
		{"invalid seed", `{"allowed_hosts":["example.org"]}`, "not SQL", nil},
		{"empty allowlist", `{}`, `CREATE TABLE sources(source_id TEXT,url TEXT);`, nil},
		{"page limit", `{"allowed_hosts":["example.org"]}`, `CREATE TABLE sources(source_id TEXT,url TEXT);`, []string{"--max-pages", "501"}},
		{"depth", `{"allowed_hosts":["example.org"]}`, `CREATE TABLE sources(source_id TEXT,url TEXT);`, []string{"--depth", "3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "config.json")
			seed := filepath.Join(root, "seed.sql")
			output := filepath.Join(root, "report.json")
			for path, data := range map[string]string{config: tc.config, seed: tc.seed} {
				if err := os.WriteFile(path, []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"--config", config, "--seed", seed, "--state", filepath.Join(root, "state.sqlite"), "--output", output}, tc.extra...)
			var stdout, stderr bytes.Buffer
			if err := Discover(context.Background(), args, &stdout, &stderr); err == nil {
				t.Fatal("invalid input accepted")
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("report should not exist: %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("success output on failure: %s", stdout.String())
			}
		})
	}
}

func TestDiscoverEmptyCrawlWritesReviewReportAndReturnsFailure(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.json")
	seed := filepath.Join(root, "seed.sql")
	output := filepath.Join(root, "nested/report.json")
	for path, data := range map[string]string{config: `{"allowed_hosts":["example.org"],"start_urls":[]}`, seed: `CREATE TABLE sources(source_id TEXT,url TEXT);`} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	err := Discover(context.Background(), []string{"--config", config, "--seed", seed, "--state", filepath.Join(root, "state.sqlite"), "--output", output}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no pages successfully fetched") {
		t.Fatalf("error=%v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report scraper.Report
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if !report.ReviewRequired || report.Pages == nil || report.Errors == nil || report.Candidates == nil || report.Attempted != 0 {
		t.Fatalf("report: %s", data)
	}
	if stdout.String() != "pages=0 candidates=0 errors=0\n" {
		t.Fatalf("summary: %s", stdout.String())
	}
}
