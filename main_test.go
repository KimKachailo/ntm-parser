package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadNoticeNumbersStopsAtEOF(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	numbers, err := readNoticeNumbers(strings.NewReader("2269(P)/26\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if len(numbers) != 1 || numbers[0] != "2269(P)/26" {
		t.Fatalf("numbers = %v, want [2269(P)/26]", numbers)
	}
}

func TestReadNoticeNumbersStopsAtEmptyLine(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	numbers, err := readNoticeNumbers(
		strings.NewReader("2269(P)/26\n1848(T)/26\n\n9999/26\n"),
		&output,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(numbers) != 2 || numbers[0] != "2269(P)/26" || numbers[1] != "1848(T)/26" {
		t.Fatalf("numbers = %v", numbers)
	}
}

func TestSplitNoticeArguments(t *testing.T) {
	t.Parallel()

	got := splitNoticeArguments([]string{"2269(P)/26,", "1848(T)/26;\t42/26"})
	want := []string{"2269(P)/26", "1848(T)/26", "42/26"}
	if len(got) != len(want) {
		t.Fatalf("split result = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("result[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
