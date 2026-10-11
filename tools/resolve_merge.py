#!/usr/bin/env python3
"""Резолвер конфликтов при автослиянии веток Claude Routine (claude/*) в main.

Routine всегда работает только в content/ (посты, sources.json, очередь), поэтому
единственные ожидаемые конфликты — текстовые в content/queue.json и .gitignore
(несколько сессий дописывают их одновременно, диффы пересекаются по контексту) и
add/add по бинарным файлам, если две сессии независимо выбрали один и тот же id
поста (дубль темы на одну дату) — такие уже разрешены в workflow через `--ours`.

Запускается из .github/workflows/merge-claude-branches.yml:
  resolve_merge.py conflicts <файл1> <файл2> ...   — резолвит текстовые конфликты
  resolve_merge.py dedupe                           — финальная проверка дублей id
    (запускается всегда, даже после «чистого» слияния: git иногда сливает
    queue.json без конфликта, но с задвоенной записью, если правки не пересеклись
    по строкам).
"""
import json
import subprocess
import sys

QUEUE = "content/queue.json"
GITIGNORE = ".gitignore"


def show(rev):
    try:
        return subprocess.check_output(["git", "show", rev])
    except subprocess.CalledProcessError:
        return None


def write_queue(items):
    with open(QUEUE, "w", encoding="utf-8") as f:
        json.dump(items, f, ensure_ascii=False, indent=2)
        f.write("\n")


def resolve_queue_conflict():
    ours_raw, theirs_raw = show(f":2:{QUEUE}"), show(f":3:{QUEUE}")
    if ours_raw is None or theirs_raw is None:
        return
    ours, theirs = json.loads(ours_raw), json.loads(theirs_raw)
    by_id, order = {}, []
    for it in ours:
        by_id[it["id"]] = it
        order.append(it["id"])
    added = 0
    for it in theirs:
        if it["id"] not in by_id:
            by_id[it["id"]] = it
            order.append(it["id"])
            added += 1
    write_queue([by_id[i] for i in order])
    print(f"resolve_merge: queue.json union — ours={len(ours)} +{added} new = {len(order)}")


def resolve_gitignore_conflict():
    lines, seen = [], set()
    for rev in (f":2:{GITIGNORE}", f":3:{GITIGNORE}"):
        raw = show(rev)
        if raw is None:
            continue
        for l in raw.decode().splitlines():
            if l.strip() and l not in seen:
                seen.add(l)
                lines.append(l)
    with open(GITIGNORE, "w") as f:
        f.write("\n".join(lines) + "\n")
    print("resolve_merge: .gitignore merged union of lines")


def dedupe_queue():
    try:
        with open(QUEUE, encoding="utf-8") as f:
            items = json.load(f)
    except FileNotFoundError:
        return
    seen, out = set(), []
    for it in items:
        if it["id"] in seen:
            continue
        seen.add(it["id"])
        out.append(it)
    if len(out) != len(items):
        write_queue(out)
        print(f"resolve_merge: dedupe removed {len(items) - len(out)} duplicate id(s)")


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "conflicts":
        for path in sys.argv[2:]:
            if path == QUEUE:
                resolve_queue_conflict()
            elif path == GITIGNORE:
                resolve_gitignore_conflict()
            else:
                print(f"resolve_merge: no text resolver for {path} (handle via --ours)", file=sys.stderr)
    dedupe_queue()


if __name__ == "__main__":
    main()
