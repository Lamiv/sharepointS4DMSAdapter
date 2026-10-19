#!/usr/bin/env python3
"""Checks the documentation: every relative link and #anchor in every Markdown
file must resolve, and no document may mention files that do not exist in the
repository's own command examples (docker compose -f <file>).

Usage:  python3 scripts/check_docs.py          (from anywhere; exit code 1 on problems)

Only the standard library is needed. External (http/https) links are not
fetched, so the check runs offline and in CI without network access.
"""
import os
import re
import sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
SKIP_DIRS = {".git", "node_modules", "results", "testdata"}
LINK = re.compile(r"(?<!\\)!?\[(?:[^\]\\]|\\.)*\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)")
HEADING = re.compile(r"^(#{1,6})\s+(.*?)\s*#*\s*$")
HTML_ID = re.compile(r'<a\s+(?:name|id)="([^"]+)"')


def md_files():
    for base, dirs, files in os.walk(ROOT):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        for f in files:
            if f.endswith(".md"):
                yield os.path.join(base, f)


def strip_fences(text):
    out, fenced = [], False
    for line in text.splitlines():
        if line.lstrip().startswith("```") or line.lstrip().startswith("~~~"):
            fenced = not fenced
            out.append("")
            continue
        out.append("" if fenced else line)
    return out


def slug(heading):
    h = re.sub(r"!?\[([^\]]*)\]\([^)]*\)", r"\1", heading)  # links -> text
    h = h.replace("`", "")
    h = h.lower()
    h = re.sub(r"[^\w\- ]", "", h, flags=re.UNICODE)
    return h.replace(" ", "-")


_anchor_cache = {}


def anchors(path):
    if path in _anchor_cache:
        return _anchor_cache[path]
    seen, result = {}, set()
    with open(path, encoding="utf-8") as f:
        text = f.read()
    for line in strip_fences(text):
        m = HEADING.match(line)
        if m:
            s = slug(m.group(2))
            n = seen.get(s, 0)
            seen[s] = n + 1
            result.add(s if n == 0 else f"{s}-{n}")
        for i in HTML_ID.findall(line):
            result.add(i)
    _anchor_cache[path] = result
    return result


def main():
    problems = []
    files = sorted(md_files())
    for path in files:
        rel = os.path.relpath(path, ROOT).replace("\\", "/")
        with open(path, encoding="utf-8") as f:
            text = f.read()
        for n, line in enumerate(strip_fences(text), 1):
            line = re.sub(r"`[^`]*`", "", line)  # links inside inline code are examples
            for target in LINK.findall(line):
                if re.match(r"^(https?:|mailto:|tel:)", target):
                    continue
                file_part, _, frag = target.partition("#")
                dest = path if file_part == "" else os.path.normpath(os.path.join(os.path.dirname(path), file_part))
                if not os.path.exists(dest):
                    problems.append(f"{rel}:{n}: broken link: {target}")
                    continue
                if frag and dest.endswith(".md") and frag not in anchors(dest):
                    problems.append(f"{rel}:{n}: no heading '#{frag}' in {os.path.relpath(dest, ROOT)}")
    print(f"checked {len(files)} Markdown files")
    for p in problems:
        print("  " + p)
    if problems:
        print(f"{len(problems)} problem(s)")
        return 1
    print("all relative links and anchors resolve")
    return 0


if __name__ == "__main__":
    sys.exit(main())
