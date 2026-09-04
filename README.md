# NtM Parser

NtM Parser extracts Temporary and Preliminary notices from the weekly
[Admiralty Notices to Mariners](https://msi.admiralty.co.uk/NoticesToMariners/Weekly)
PDF publications and writes the results to a DOCX file.

## Requirements

- Go 1.26.6 or newer
- [`pdftotext`](https://poppler.freedesktop.org/) from Poppler, available in
  `PATH`
- Network access to `msi.admiralty.co.uk`

## Usage

Pass one or more notice numbers separated by spaces, commas, or semicolons:

```console
go run . "2269(P)/26" "1848(T)/26"
go run . "2269(P)/26, 1848(T)/26"
```

Run without arguments to enter notice numbers interactively, one per line:

```console
go run .
```

Submit an empty line or EOF to start the search. Notice numbers use forms such
as `2269(P)/26`, `1848(T)/26`, or `1234/2026`.

The result is written to the current directory as:

```text
ntm_notices_YYYYMMDD_HHMMSS.docx
```

Existing files are never overwritten; a numeric suffix is added on a filename
collision. The document records the source week for each result and
distinguishes a genuinely missing notice from a technical search error.

## How it works

For each notice year, the program searches weekly publications from the latest
applicable ISO week backwards. Weekly PDFs are downloaded and converted to text
once per run, even when several requested notices belong to the same issue.
Up to four notice searches run concurrently.

HTTP requests and `pdftotext` have timeouts. HTML and PDF response sizes are
bounded, and downloaded files are checked for a PDF signature before being
passed to Poppler.

## Build and checks

```console
go build .
go test ./...
go vet ./...
```

On Windows, `go build .` creates `ntm-parser.exe` in the current directory.
To run the optional live website/PDF check, set `NTM_INTEGRATION=1` and run
`go test -run TestAdmiraltyIntegration`.

## Failure behavior

`NOT FOUND` means the available weekly PDFs were checked successfully and did
not contain the requested notice. Network, website, file, and PDF conversion
failures are reported separately, included in the DOCX as `ERROR` entries, and
cause a non-zero process exit status after partial results are saved.
