package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/charliewilco/keystoner/tools/publisher"
)

// Publish adapts flags and output to the catalog publisher without exiting.
func Publish(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("publish", flag.ContinueOnError)
	flags.SetOutput(stderr)
	seed := flags.String("seed", "seed/catalog.sql", "Trusted catalog SQL")
	output := flags.String("output", "catalog", "Published catalog directory")
	release := flags.String("release", "", "New immutable release ID (required)")
	published := flags.String("published-at", "", "RFC3339 UTC timestamp (required)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *release == "" || *published == "" {
		return fmt.Errorf("--release and --published-at are required")
	}
	m, err := publisher.Publish(ctx, *seed, *output, *release, *published)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}
