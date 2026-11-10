package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
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
