# Changelog

## [Unreleased]

### Fixed

**Boundary detection — next notice number captured in output**

Problem: regex pattern `\r?\n\s*` before the next notice number wasn't
sufficient. The PDF contains 4-5 empty lines (`\r\n\r\n\r\n\r\n\r\n`)
between notices, and notice numbers inside text (e.g. `Former Notice
4572(P)/24`) were also matching the boundary pattern.

Solution: replaced regex-based boundary detection with line-by-line
parsing. A boundary is detected when 2+ consecutive empty lines are
followed by a line starting with `^\d{4}` (no indentation). Numbers
inside notice text always have leading whitespace — they never trigger
the boundary condition.

---

**Multi-page notices — (continued) block not captured**

Problem: long notices continue on the next page with a header line:
`2269(P)/26   MEXICO - ... (continued)`. The initial marker search
used `noticeNumber + " (continued)"` which didn't match because
`(continued)` comes after the full title, not immediately after the number.

Solution: scan all lines for any line containing both `noticeNumber`
and `(continued)` — use that full line as the marker. Strip the header
line from the continued block before appending to the main block.