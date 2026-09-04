# Changelog

## [Unreleased]

### Added

- Local CLI and interactive input with validated notice-number formats.
- DOCX output containing source week/year metadata and explicit error entries.
- Unit tests for input parsing, HTML extraction, ISO week ranges, notice
  boundaries, multi-page continuations, shared PDF caching, response validation,
  and DOCX structure.
- Cancellation and timeouts for HTTP requests and `pdftotext`.
- HTML/PDF size limits and PDF signature validation.
- Go 1.26.6 minimum to include standard-library security fixes.

### Changed

- Replaced the Telegram bot entry point with a local CLI.
- Weekly PDF cache now stores extracted text and coalesces concurrent loads.
- Limited concurrent notice searches to four workers.
- Past years search all 52 or 53 ISO weeks; the current year searches through
  the current ISO week.
- Notice headers must begin at the start of a line and match a complete notice
  number. Body references no longer count as matches.
- All continuation sections are appended instead of handling only the first.
- DOCX files use collision-safe names and are removed if finalization fails.

### Fixed

- Technical network, file, website, and conversion failures are no longer
  reported as `NOT FOUND`.
- Interactive input now handles EOF and scanner errors correctly.
- HTTP response statuses, temporary-file writes, archive finalization, and file
  close errors are checked.
- XML-invalid control characters are removed before DOCX generation.
- Numbered DOCX items with more than one digit are recognized.
