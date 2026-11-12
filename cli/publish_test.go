package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"path/filepath"
	"testing"

	"github.com/charliewilco/keystoner/core"
)

func TestPublishCLIInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"--unknown"}, {"extra"}, {"--release", "one"}, {"--published-at", "2026-11-12T12:00:00Z"}} {
		var stdout, stderr bytes.Buffer
		err := Publish(context.Background(), args, &stdout, &stderr)
		if err == nil || stdout.Len() != 0 {
			t.Fatalf("args=%v err=%v stdout=%s", args, err, stdout.String())
		}
		if len(args) > 0 && args[0] == "--help" && !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("help: %v", err)
		}
	}
}

func TestPublishCLIProducesManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "catalog")
	var stdout, stderr bytes.Buffer
	args := []string{"--seed", "../seed/catalog.sql", "--output", root, "--release", "cli-test", "--published-at", "2026-11-12T12:00:00Z"}
	if err := Publish(context.Background(), args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var m core.Manifest
	if err := json.Unmarshal(stdout.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.DB.Close()
	if m != c.Manifest || stderr.Len() != 0 {
		t.Fatalf("manifest=%+v stderr=%s", m, stderr.String())
	}
	stdout.Reset()
	if err := Publish(context.Background(), args, &stdout, &stderr); err == nil || stdout.Len() != 0 {
		t.Fatalf("overwrite: err=%v stdout=%s", err, stdout.String())
	}
}
