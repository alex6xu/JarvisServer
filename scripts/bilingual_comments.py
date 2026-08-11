#!/usr/bin/env python3
"""Add Chinese translations to English Go comments while keeping English."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
import time
from pathlib import Path

from deep_translator import GoogleTranslator

CJK_RE = re.compile(r"[\u4e00-\u9fff]")
LATIN_RE = re.compile(r"[A-Za-z]{3,}")
GO_DIRECTIVE_RE = re.compile(r"^\s*//go:")
FULL_COMMENT_RE = re.compile(r"^(?P<indent>\s*)//(?P<body>.*)$")
INLINE_COMMENT_RE = re.compile(r"(?P<code>.*?)(?P<ws>\s+)//(?P<body>.*)$")
BLOCK_COMMENT_START = re.compile(r"/\*")
BLOCK_COMMENT_END = re.compile(r"\*/")
CACHE_VERSION = 1


def has_cjk(text: str) -> bool:
    return bool(CJK_RE.search(text))


def has_latin(text: str) -> bool:
    return bool(LATIN_RE.search(text))


def is_bilingual(body: str) -> bool:
    body = body.strip()
    if not body:
        return True
    if " | " in body and has_cjk(body) and has_latin(body):
        return True
    if body.startswith("EN:") or body.startswith("中文:"):
        return True
    return has_cjk(body) and has_latin(body)


def should_skip_body(body: str) -> bool:
    stripped = body.strip()
    if not stripped:
        return True
    if stripped.startswith("+build") or stripped.startswith("go:"):
        return True
    if is_bilingual(stripped):
        return True
    if has_cjk(stripped) and not has_latin(stripped):
        return True
    if not has_latin(stripped):
        return True
    return False


def cache_key(text: str) -> str:
    payload = f"{CACHE_VERSION}\n{text}"
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def load_cache(path: Path) -> dict[str, str]:
    if not path.exists():
        return {}
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return {}


def save_cache(path: Path, cache: dict[str, str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(cache, ensure_ascii=False, indent=2), encoding="utf-8")


def translate_text(text: str, cache: dict[str, str], translator: GoogleTranslator) -> str:
    key = cache_key(text)
    if key in cache:
        return cache[key]
    for attempt in range(5):
        try:
            zh = translator.translate(text)
            cache[key] = zh
            time.sleep(0.05)
            return zh
        except Exception:
            time.sleep(0.5 * (attempt + 1))
    raise RuntimeError(f"translation failed: {text[:80]!r}")


def split_block_comment_lines(text: str) -> list[str]:
    lines = text.splitlines(keepends=True)
    if not lines:
        return []
    return lines


def process_full_line_comment(line: str, cache: dict[str, str], translator: GoogleTranslator) -> list[str]:
    m = FULL_COMMENT_RE.match(line.rstrip("\n"))
    if not m:
        return [line]
    indent, body = m.group("indent"), m.group("body")
    if GO_DIRECTIVE_RE.match(line):
        return [line]
    if should_skip_body(body):
        return [line]

    english = body.strip()
    zh = translate_text(english, cache, translator)
    newline = "\n" if line.endswith("\n") else ""
    return [
        f"{indent}// EN: {english}{newline}",
        f"{indent}// 中文: {zh}{newline}",
    ]


def process_inline_comment(line: str, cache: dict[str, str], translator: GoogleTranslator) -> str:
    if GO_DIRECTIVE_RE.search(line):
        return line
    in_string = False
    quote = ""
    escape = False
    best = None
    for i, ch in enumerate(line):
        if in_string:
            if escape:
                escape = False
                continue
            if ch == "\\":
                escape = True
                continue
            if ch == quote:
                in_string = False
            continue
        if ch in "\"'`":
            in_string = True
            quote = ch
            continue
        if ch == "/" and i + 1 < len(line) and line[i + 1] == "/":
            best = i
            break
    if best is None:
        return line

    code = line[:best].rstrip()
    comment_part = line[best:]
    m = re.match(r"//(?P<body>.*)$", comment_part)
    if not m:
        return line
    body = m.group("body")
    if should_skip_body(body):
        return line

    english = body.strip()
    zh = translate_text(english, cache, translator)
    newline = "\n" if line.endswith("\n") else ""
    return f"{code} // EN: {english} | 中文: {zh}{newline}"


def process_block_comment_block(lines: list[str], cache: dict[str, str], translator: GoogleTranslator) -> list[str]:
    if not lines:
        return lines
    joined = "".join(lines)
    if should_skip_body(joined):
        return lines

    english_lines: list[str] = []
    prefix = ""
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("/*"):
            prefix = line.split("/*", 1)[0]
            rest = stripped[2:]
            if rest.endswith("*/"):
                rest = rest[:-2]
            english_lines.append(rest.strip())
        elif stripped.endswith("*/"):
            english_lines.append(stripped[:-2].strip())
        else:
            english_lines.append(stripped.lstrip("* ").rstrip())

    english = "\n".join(x for x in english_lines if x).strip()
    if not english or not has_latin(english):
        return lines
    if has_cjk(english) and not has_latin(english):
        return lines

    zh = translate_text(english, cache, translator)
    indent = prefix
    out = [f"{indent}/* EN: {english_lines[0]}\n"]
    for part in english_lines[1:]:
        if part:
            out.append(f"{indent} * {part}\n")
    out.append(f"{indent} * 中文: {zh.replace(chr(10), ' ')}\n")
    out.append(f"{indent} */\n")
    return out


def process_file(path: Path, cache: dict[str, str], translator: GoogleTranslator, dry_run: bool) -> bool:
    original = path.read_text(encoding="utf-8")
    lines = original.splitlines(keepends=True)
    out: list[str] = []
    changed = False
    i = 0
    while i < len(lines):
        line = lines[i]
        stripped = line.lstrip()

        if stripped.startswith("/*"):
            block = [line]
            i += 1
            while i < len(lines) and "*/" not in block[-1]:
                block.append(lines[i])
                i += 1
            new_block = process_block_comment_block(block, cache, translator)
            if new_block != block:
                changed = True
            out.extend(new_block)
            continue

        if stripped.startswith("//"):
            new_lines = process_full_line_comment(line, cache, translator)
            if len(new_lines) != 1:
                changed = True
            out.extend(new_lines)
            i += 1
            continue

        if "//" in line and not stripped.startswith("//"):
            new_line = process_inline_comment(line, cache, translator)
            if new_line != line:
                changed = True
            out.append(new_line)
            i += 1
            continue

        out.append(line)
        i += 1

    if changed and not dry_run:
        path.write_text("".join(out), encoding="utf-8")
    return changed


def restore_btw_from_git(root: Path) -> None:
    files = [
        "internal/cli/btw/btw.go",
        "internal/cli/btw/btw_config.go",
    ]
    for rel in files:
        path = root / rel
        if not path.exists():
            continue
        try:
            content = subprocess.check_output(
                ["git", "show", f"HEAD:{rel}"],
                cwd=root,
                text=True,
                encoding="utf-8",
            )
            path.write_text(content, encoding="utf-8")
        except subprocess.CalledProcessError:
            pass


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default=".", type=Path)
    parser.add_argument("--cache", default=".cache/comment_zh.json", type=Path)
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--restore-btw", action="store_true")
    parser.add_argument("--file", action="append", dest="files")
    args = parser.parse_args()

    root = args.root.resolve()
    cache_path = (root / args.cache).resolve()
    cache = load_cache(cache_path)
    translator = GoogleTranslator(source="en", target="zh-CN")

    if args.restore_btw:
        restore_btw_from_git(root)

    if args.files:
        go_files = [root / f for f in args.files]
    else:
        go_files = sorted(root.rglob("*.go"))
        go_files = [p for p in go_files if "vendor" not in p.parts and ".git" not in p.parts]

    changed_files = 0
    for idx, path in enumerate(go_files, 1):
        try:
            if process_file(path, cache, translator, args.dry_run):
                changed_files += 1
                print(f"[{idx}/{len(go_files)}] updated {path.relative_to(root)}")
        except Exception as exc:
            print(f"ERROR {path}: {exc}", file=sys.stderr)
            save_cache(cache_path, cache)
            return 1
        if idx % 20 == 0:
            save_cache(cache_path, cache)

    save_cache(cache_path, cache)
    print(f"Done. Changed {changed_files}/{len(go_files)} files.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
