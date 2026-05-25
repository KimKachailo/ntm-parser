# NtM Parser

Automatically extracts Temporary & Preliminary (T&P) notices from Admiralty
Notices to Mariners weekly publications.

Parses the [Admiralty NtM website](https://msi.admiralty.co.uk/NoticesToMariners/Weekly),
downloads the relevant weekly PDF, and extracts notice text by number.

## Requirements

- Go 1.21+
- [pdftotext](https://poppler.freedesktop.org/) (poppler-utils)

## Usage

Edit the `queries` slice in `main.go` with your notice numbers and weeks:

```go
queries := []noticeQuery{
    {"2026", "20", "2269(P)/26"},
    {"2026", "20", "2242(P)/26"},
}
```

Then run:

    go run main.go

Results are saved to `notices.csv`.