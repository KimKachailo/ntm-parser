package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeNoticeNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{input: "2269(P)/26", want: "2269(P)/26"},
		{input: " 42(t)/2025 ", want: "42(T)/2025"},
		{input: "1/26", want: "1/26"},
	}
	for _, test := range tests {
		got, err := normalizeNoticeNumber(test.input)
		if err != nil {
			t.Fatalf("normalizeNoticeNumber(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Errorf("normalizeNoticeNumber(%q) = %q, want %q", test.input, got, test.want)
		}
	}

	for _, input := range []string{"", "2269(P)/", "2269(X)/26", "2269(P)/026", "notice/26"} {
		if _, err := normalizeNoticeNumber(input); err == nil {
			t.Errorf("normalizeNoticeNumber(%q) unexpectedly succeeded", input)
		}
	}
}

func TestWeeksToSearch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	_, currentWeek := now.ISOWeek()
	current, err := weeksToSearch("2026", now)
	if err != nil {
		t.Fatal(err)
	}
	if current[0] != currentWeek || current[len(current)-1] != 1 {
		t.Fatalf("current-year weeks = %v...%v, want %d...1", current[0], current[len(current)-1], currentWeek)
	}

	past, err := weeksToSearch("2020", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 53 || past[0] != 53 || past[len(past)-1] != 1 {
		t.Fatalf("2020 weeks = len %d, %d...%d; want 53, 53...1", len(past), past[0], past[len(past)-1])
	}

	if _, err := weeksToSearch("2027", now); err == nil {
		t.Fatal("future year unexpectedly accepted")
	}
}

func TestExtractNoticeWithMultipleContinuations(t *testing.T) {
	t.Parallel()

	text := `2269(P)/26 .... 12
Former Notice 2269(P)/26 is mentioned here.

2269(P)/26 MEXICO - Main title
    Main body
    Former Notice 1000/25
Wk20/26
20.3

2269(P)/26 MEXICO - Main title (continued)
    First continuation

1234(T)/26 ANOTHER NOTICE
    Other body

2269(P)/26 MEXICO - Main title
(continued)
    Second continuation

9999/26 FINAL NOTICE
    Final body`

	got, err := extractNotice(text, "2269(p)/26")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"2269(P)/26 MEXICO - Main title",
		"Main body",
		"First continuation",
		"Second continuation",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("result does not contain %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{".... 12", "Wk20/26", "(continued)", "Other body", "Final body"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("result unexpectedly contains %q:\n%s", unwanted, got)
		}
	}
}

func TestExtractNoticeRejectsBodyReference(t *testing.T) {
	t.Parallel()

	_, err := extractNotice("Former Notice 2269(P)/26 has been cancelled.", "2269(P)/26")
	if !errors.Is(err, errNoticeNotInPDF) {
		t.Fatalf("error = %v, want errNoticeNotInPDF", err)
	}
}

func TestExtractHTMLFields(t *testing.T) {
	t.Parallel()

	token, err := extractCSRFToken(strings.NewReader(`
		<form><input name="__RequestVerificationToken" value="first"></form>
		<form><input name="__RequestVerificationToken" value=""></form>`))
	if err != nil {
		t.Fatal(err)
	}
	if token != "first" {
		t.Fatalf("token = %q, want first", token)
	}

	fileName, batchID, err := extractFileInfo(strings.NewReader(
		`<a href="/NoticesToMariners/DownloadFile?fileName=WKNM-test.pdf&amp;batchId=batch-1">PDF</a>`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if fileName != "WKNM-test.pdf" || batchID != "batch-1" {
		t.Fatalf("file info = %q, %q", fileName, batchID)
	}
}

func TestPDFCacheLoadsOnce(t *testing.T) {
	t.Parallel()

	cache := newPDFCache()
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	loader := func() (string, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return "pdf text", nil
	}

	const goroutines = 12
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			got, err := cache.getOrLoad(context.Background(), "2026-20", loader)
			if err != nil {
				t.Errorf("getOrLoad: %v", err)
			}
			if got != "pdf text" {
				t.Errorf("getOrLoad = %q", got)
			}
		}()
	}
	<-entered
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("loader called %d times, want 1", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDownloadPDFValidation(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": {"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html>not a PDF</html>")),
			Request:    req,
		}, nil
	})}

	if _, err := downloadPDFToMemory(context.Background(), client, "wknm.pdf", "batch"); err == nil {
		t.Fatal("non-PDF response unexpectedly accepted")
	}
}
