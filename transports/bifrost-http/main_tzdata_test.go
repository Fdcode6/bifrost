package main

import (
	"os"
	"strings"
	"testing"
)

func TestMainEmbedsTZData(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	if !strings.Contains(string(source), `_ "time/tzdata"`) {
		t.Fatalf("main.go must blank-import time/tzdata so container images without /usr/share/zoneinfo can load Asia/Shanghai")
	}
}
