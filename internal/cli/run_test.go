package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestVersionAndInvalidCommands(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"version"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var value map[string]string
	if err := json.Unmarshal(out.Bytes(), &value); err != nil || value["name"] != "HopSend" {
		t.Fatal(out.String(), err)
	}
	for _, args := range [][]string{{"unknown"}, {"version", "extra"}, {"doctor", "--unknown"}} {
		if code := Run(context.Background(), args, &out, &stderr); code != 2 {
			t.Fatalf("%v: got %d", args, code)
		}
	}
}
