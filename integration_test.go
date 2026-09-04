package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAdmiraltyIntegration(t *testing.T) {
	if os.Getenv("NTM_INTEGRATION") != "1" {
		t.Skip("set NTM_INTEGRATION=1 to run the live Admiralty test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, token, err := initClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fileName, batchID, err := fetchFileInfo(ctx, client, token, "2026", "20")
	if err != nil {
		t.Fatal(err)
	}
	data, err := downloadPDFToMemory(ctx, client, fileName, batchID)
	if err != nil {
		t.Fatal(err)
	}
	text, err := extractPDFText(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	notice, err := extractNotice(text, "2269(P)/26")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, "2269(P)/26") {
		t.Fatalf("unexpected notice text: %s", notice)
	}
}
