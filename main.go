package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

const (
	baseURL               = "https://msi.admiralty.co.uk/NoticesToMariners/Weekly"
	httpTimeout           = 45 * time.Second
	maxConcurrentSearches = 4
)

func initClient(ctx context.Context) (*http.Client, string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, "", fmt.Errorf("create cookie jar: %w", err)
	}

	client := &http.Client{
		Jar:     jar,
		Timeout: httpTimeout,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create base-page request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("get base page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("get base page: unexpected HTTP status %s", resp.Status)
	}

	body, err := readLimited(resp.Body, maxHTMLSize, "base page")
	if err != nil {
		return nil, "", err
	}
	token, err := extractCSRFToken(strings.NewReader(string(body)))
	if err != nil {
		return nil, "", fmt.Errorf("extract CSRF token: %w", err)
	}

	return client, token, nil
}

type searchJob struct {
	index  int
	number string
}

func searchNotices(ctx context.Context, client *http.Client, token string, numbers []string) []noticeResult {
	results := make([]noticeResult, len(numbers))
	cache := newPDFCache()
	jobs := make(chan searchJob)

	workerCount := min(maxConcurrentSearches, len(numbers))
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for range workerCount {
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := ctx.Err(); err != nil {
					results[job.index] = noticeResult{number: job.number, err: err}
					continue
				}
				results[job.index] = findNotice(ctx, client, token, job.number, cache, time.Now())
			}
		}()
	}

	for i, number := range numbers {
		jobs <- searchJob{index: i, number: number}
	}
	close(jobs)
	wg.Wait()

	return results
}

func runCLI(ctx context.Context, numbers []string) error {
	if len(numbers) == 0 {
		return errors.New("no notice numbers provided")
	}

	normalized := make([]string, len(numbers))
	for i, number := range numbers {
		n, err := normalizeNoticeNumber(number)
		if err != nil {
			return err
		}
		normalized[i] = n
	}

	client, token, err := initClient(ctx)
	if err != nil {
		return fmt.Errorf("connection error: %w", err)
	}

	results := searchNotices(ctx, client, token, normalized)
	filePath, err := saveResultsToDocx(results)
	if err != nil {
		return fmt.Errorf("save DOCX: %w", err)
	}
	fmt.Println("Saved:", filePath)

	failed := 0
	for _, result := range results {
		if result.err != nil && !errors.Is(result.err, errNoticeNotFound) {
			failed++
			fmt.Fprintf(os.Stderr, "%s: %v\n", result.number, result.err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d notice search(es) failed; partial results were saved", failed)
	}

	return nil
}

func splitNoticeArguments(args []string) []string {
	raw := strings.Join(args, " ")
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	})

	numbers := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			numbers = append(numbers, part)
		}
	}
	return numbers
}

func readNoticeNumbers(r io.Reader, w io.Writer) ([]string, error) {
	fmt.Fprintln(w, "NtM Parser")
	fmt.Fprintln(w, "Enter notice numbers (one per line, empty line to search):")

	var numbers []string
	scanner := bufio.NewScanner(r)
	for {
		fmt.Fprint(w, "> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return nil, fmt.Errorf("read input: %w", err)
			}
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}
		numbers = append(numbers, line)
	}

	return numbers, nil
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	var numbers []string
	if len(args) > 0 {
		numbers = splitNoticeArguments(args)
	} else {
		var err error
		numbers, err = readNoticeNumbers(stdin, stdout)
		if err != nil {
			return err
		}
	}
	if len(numbers) == 0 {
		return errors.New("no notice numbers provided")
	}

	return runCLI(ctx, numbers)
}

func main() {
	log.SetFlags(0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		log.Printf("Error: %v", err)
		os.Exit(1)
	}
}
