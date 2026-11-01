package pr2

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	args := []string{"generate", "--orders", "5", "--products", "3", "--max-items", "3", "--out", dir}
	var output bytes.Buffer
	if err := Run(args, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(dir, "orders.jsonl")
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(args, strings.NewReader(""), &output); err == nil {
		t.Fatal("overwrote existing data")
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("existing orders changed")
	}
}

func TestStreamingCommands(t *testing.T) {
	var mapped, reduced bytes.Buffer
	if err := Run([]string{"map", "--algorithm", "pairs"}, strings.NewReader(`{"products":["A","B","B"]}`), &mapped); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"reduce", "--algorithm", "pairs"}, &mapped, &reduced); err != nil {
		t.Fatal(err)
	}
	if reduced.String() != "[\"A\",\"B\"]\t1\n[\"B\",\"A\"]\t1\n" {
		t.Fatalf("unexpected output: %q", reduced.String())
	}
	if err := Run([]string{"generate", "--orders", "-1"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("accepted negative orders")
	}
}
