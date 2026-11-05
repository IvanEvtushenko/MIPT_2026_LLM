"""PDF учебника -> corpus.jsonl (block_id, page, section, text).

Детерминированное извлечение без моделей. Формулы линеаризуются по геометрии PDF:
дробная черта -> (числитель)/(знаменатель), короткая черта над символом -> z̄,
корень -> √(...), мелкие глифы со смещением базовой линии -> ^{...} и _{...}.
"""
import argparse
import hashlib
import json
import re
from dataclasses import dataclass

import fitz

CMEX = {
    "\x00": "(", "\x01": ")", "\x02": "[", "\x03": "]", "\x08": "{", "\t": "}", "\x0c": "|",
    "\x10": "(", "\x11": ")", "\x12": "(", "\x13": ")", "\x14": "[", "\x15": "]", "\x1a": "{",
    "\x1b": "}", "\x1e": "/", " ": "(", "!": ")", "\"": "[", "#": "]", "(": "{", ")": "}",
    "H": "∮", "I": "∮", "P": "∑", "X": "∑", "Q": "∏", "Y": "∏", "R": "∫", "Z": "∫",
    "S": "⋃", "[": "⋃", "T": "⋂", "\\": "⋂", "b": "^", "e": "~", "n": "{", "o": "}",
    "p": "√", "q": "√", "r": "√", "s": "√", "t": "√",
    "": "(", "": ")", "": "[", "": "]", "": "{", "": "}",
}
MSBM = {"C": "ℂ", "D": "𝔻", "T": "𝕋", "R": "ℝ", "Z": "ℤ", "S": "𝕊", "N": "ℕ", "F": "𝔽", "κ": "ϰ"}
TEXT_FIX = {"\x15": "–", "\x16": "—", "\x01": "", "џ": "§", "Ѕ": "«", "“": "»"}
HEADER_BASE = 40.0
# высота глифа CMEX в em: origin висит над базовой линией на (высота / 2 + ось 0.25 em)
CMEX_EM = {**dict.fromkeys("\x00\x01\x02\x03\x08\tno p", 1.2), **dict.fromkeys("\x10\x11q", 1.8),
           **dict.fromkeys("\x12\x13\x14\x15\x1a\x1b\x1er", 2.4), **dict.fromkeys(" !\"#()s", 3.0),
           **dict.fromkeys("XY[\\", 1.4), **dict.fromkeys("ZI", 2.22), **dict.fromkeys("PQST", 1.0),
           **dict.fromkeys("RH", 1.11), "\x0c": 0.6}
DISPLAY_OPS = set("XYZI[\\")


@dataclass
class Item:
    text: str
    x0: float
    x1: float
    base: float
    size: float
    font: str
    op: object = None

    @property
    def cx(self):
        return (self.x0 + self.x1) / 2

    @property
    def small(self):
        return self.size < 8.5 and not self.free

    @property
    def free(self):
        return self.font.startswith("CMEX")


def map_char(c, font):
    fam = font.rstrip("0123456789")
    if fam == "CMEX":
        return CMEX.get(c, "" if (ord(c) < 0x20 or 0xE000 <= ord(c) <= 0xF8FF) else c)
    if fam == "MSBM":
        return MSBM.get(c, c)
    if fam.startswith(("CM", "MS")):
        return "" if ord(c) < 0x20 else c
    if c in TEXT_FIX:
        return TEXT_FIX[c]
    if 0x80 <= ord(c) <= 0xFF:
        c = bytes([ord(c)]).decode("cp1251", errors="replace")
        return TEXT_FIX.get(c, c)
    return "" if ord(c) < 0x20 else c


def page_items(page):
    items = []
    for block in page.get_text("rawdict")["blocks"]:
        for line in block.get("lines", []):
            for span in line["spans"]:
                for ch in span["chars"]:
                    t = map_char(ch["c"], span["font"])
                    if not t or t.isspace() or ch["origin"][1] < HEADER_BASE:
                        continue
                    x0, _, x1, _ = ch["bbox"]
                    base = ch["origin"][1]
                    font = span["font"]
                    if font.startswith("CMEX"):
                        em = CMEX_EM.get(ch["c"], 1.2)
                        base += (em / 2 + 0.25) * span["size"]
                        if ch["c"] in DISPLAY_OPS:
                            font = "CMEXOP"
                    items.append(Item(t, x0, x1, base, span["size"], font))
    bars = [it for it in items if it.font.startswith("CMEX") and it.text == "|"]
    keep = []
    for it in sorted(bars, key=lambda it: (round(it.x0), it.base)):
        if keep and abs(keep[-1][-1].x0 - it.x0) < 0.6 and it.base - keep[-1][-1].base < 7:
            keep[-1].append(it)
        else:
            keep.append([it])
    items = [it for it in items if it not in bars]
    for grp in keep:
        it = grp[0]
        it.base = (grp[0].base + grp[-1].base) / 2
        items.append(it)
    return items


def page_rules(page):
    rules = []
    for d in page.get_drawings():
        for it in d["items"]:
            if it[0] == "l" and abs(it[1].y - it[2].y) < 0.5 and abs(it[2].x - it[1].x) > 1.5:
                rules.append((min(it[1].x, it[2].x), max(it[1].x, it[2].x), it[1].y))
            elif it[0] == "re" and it[1].height < 1.2 and it[1].width > 1.5:
                rules.append((it[1].x0, it[1].x1, (it[1].y0 + it[1].y1) / 2))
    return sorted(rules, key=lambda r: r[1] - r[0])


def simple(s):
    return bool(re.fullmatch(r"[∂d]?\w[\u0304\u0307′]*(_\w|\^\d)?|\d+|\w!|\w′?\([^()\s]*\)", s)) or (
        s.startswith("(") and s.endswith(")") and s.count("(") == 1)


def wrap(s):
    return s if simple(s) else f"({s})"


def emit_script(run, base):
    sup = sorted((it for it in run if it.base < base - 0.8), key=lambda it: it.x0)
    sub = sorted((it for it in run if it.base > base + 0.8), key=lambda it: it.x0)
    mid = sorted((it for it in run if abs(it.base - base) <= 0.8), key=lambda it: it.x0)
    out = "".join(it.text for it in mid)
    if sub:
        t = join_tight(sub)
        out += "_" + (t if len(t) == 1 else "{" + t + "}")
    if sup:
        t = join_tight(sup)
        out += t if set(t) <= {"′"} else "^" + (t if len(t) == 1 else "{" + t + "}")
    return out


def join_tight(items):
    out, prev = "", None
    for it in items:
        if prev is not None and it.size < prev.size - 1 and abs(it.base - prev.base) > 0.8:
            out += ("_" if it.base > prev.base else "^") + it.text
            continue
        if prev is not None and it.x0 - prev.x1 > 0.25 * it.size:
            out += " "
        out += it.text
        prev = it
    return out


def assemble_line(items, base):
    items = sorted(items, key=lambda it: it.x0)
    out, prev, run = "", None, []

    def flush():
        nonlocal out, prev
        if run:
            out += emit_script(run, base)
            prev = max(run + ([prev] if prev else []), key=lambda it: it.x1)
            run.clear()

    for it in items:
        if it.op is not None:
            continue
        if it.small and abs(it.base - base) > 0.8:
            run.append(it)
            continue
        flush()
        if prev is not None and (it.x0 - prev.x1 > 0.2 * max(it.size, 7) or (
                prev.font == "ATOM" and "/" in prev.text and it.text[:1].isalnum()) or (
                {prev.font[:4], it.font[:4]} == {"CMR1", "CMMI"} and prev.text.isalpha() and it.text.isalpha())):
            out += " "
        out += it.text
        prev = it
        limits = [x for x in items if x.op is it]
        if limits:
            out += emit_script(limits, base)
            prev = max(limits + [it], key=lambda x: x.x1)
    flush()
    return out


def group_lines(items):
    normal = sorted((it for it in items if not (it.small or it.free) or it.font == "ATOM"), key=lambda it: it.base)
    lines = []
    for it in normal:
        if lines and abs(it.base - lines[-1]["base"]) <= 2.0:
            lines[-1]["items"].append(it)
        else:
            lines.append({"base": it.base, "items": [it]})
    ops = [it for it in items if it.font == "CMEXOP"]
    for it in sorted(items, key=lambda it: not it.free):
        if (it.small or it.free) and it.font != "ATOM":
            op = next((o for o in ops if o.x0 - 4 <= it.cx <= o.x1 + 5 and abs(it.base - o.base) <= 18
                       and abs(it.base - o.base) > 3), None) if it.small else None
            if op is not None:
                ln = min(lines, key=lambda ln: abs(op.base - ln["base"]), default=None)
                if ln is not None:
                    it.op = op
                    ln["items"].append(it)
                    continue
            best = min(lines, key=lambda ln: abs(it.base - ln["base"]), default=None)
            if best is not None and abs(it.base - best["base"]) <= (16 if it.free else 12):
                if it.free:
                    it.base = best["base"]
                best["items"].append(it)
            else:
                lines.append({"base": it.base, "items": [it]})
                lines.sort(key=lambda ln: ln["base"])
    return lines


def linearize(items):
    return " ".join(assemble_line(ln["items"], ln["base"]) for ln in group_lines(items)).strip()


def apply_rules(items, rules):
    for x0, x1, y in rules:
        span = [it for it in items if x0 - 0.5 <= it.cx <= x1 + 0.5 and not it.free]
        num = [it for it in span if y - 6.5 <= it.base <= y - 1.2]
        den = [it for it in span if y + 6.0 <= it.base <= y + 13.5]
        under = [it for it in span if y + 3.5 <= it.base <= y + 9.5]
        den = den or [it for it in span if y + 3.0 <= it.base <= y + 18 and it.font == "ATOM"]
        if num and den:
            num = [it for it in span if y - 22 <= it.base <= y - 1.2]
            top = max(it.base for it in num if not it.small)
            num = [it for it in num if it.base >= top - 7]
            den = [it for it in span if y + 3.0 <= it.base <= y + 22]
            low = min((it.base for it in den if not it.small), default=min(it.base for it in den))
            den = [it for it in den if it.base <= low + 5]
            text = f"{wrap(linearize(num))}/{wrap(linearize(den))}"
            members = num + den
            new = Item(text, x0, x1, y + 2.5, max(it.size for it in members), "ATOM")
        elif under and not num:
            rad = [it for it in items if it.text == "√" and abs(it.x1 - x0) < 3 and it not in under]
            members = [it for it in span if y + 1.0 <= it.base <= y + 12 and (not it.small or it in under or True)]
            members = [it for it in members if it in under or it.small]
            body = linearize(members)
            if rad:
                text = f"√({body})" if not simple(body) else f"√{body}"
                members += rad[:1]
                x0 = rad[0].x0
            elif len(body) == 1:
                text = body + "̄"
            else:
                text = "\\overline{" + body + "}"
            base = min(under, key=lambda it: it.size).base if under else y + 6
            base = max((it.base for it in under if not it.small), default=base)
            new = Item(text, x0, x1, base, max(it.size for it in members), "ATOM")
        else:
            continue
        ids = {id(it) for it in members}
        items = [it for it in items if id(it) not in ids] + [new]
    return items


def clean(text):
    text = text.replace("̸=", "≠").replace("̸∈", "∉").replace("̸", "")
    text = re.sub(r"(\w)- (\w)", lambda m: m.group(1) + m.group(2) if m.group(2).islower() else m.group(0), text)
    text = re.sub(r"\s+", " ", text)
    text = re.sub(r" ([,.;:)])", r"\1", text)
    text = re.sub(r"\( ", "(", text)
    return text.strip()


def page_lines(page):
    items = apply_rules(page_items(page), page_rules(page))
    out = []
    for ln in group_lines(items):
        its = sorted(ln["items"], key=lambda it: it.x0)
        first = its[0]
        math = sum(1 for it in its if it.font.startswith(("CM", "MS", "ATOM")))
        k = next((i for i, it in enumerate(its) if it.font != "F49"), len(its))
        out.append({
            "head": assemble_line(its[:k], ln["base"]) if k else "",
            "rest": assemble_line(its[k:], ln["base"]) if k else "",
            "text": assemble_line(its, ln["base"]),
            "x0": first.x0,
            "x1": max(it.x1 for it in its),
            "base": ln["base"],
            "font": first.font,
            "math": math / len(its),
        })
    return out


HEAD_RE = re.compile(r"^(Теорема|Определение|Лемма|Следствие|Замечание|Пример|Доказательство|Предложение|Упражнение)\b")


def build(pdf_path, max_chars):
    doc = fitz.open(pdf_path)
    margin = 43.7
    blocks, section, subsection = [], "", ""
    for pno in range(1, doc.page_count):
        paras = []
        for ln in page_lines(doc[pno]):
            t = ln["text"]
            if ln["font"] == "F49" and (t.startswith("§") or re.match(r"^\d+\.\d+\.", t)):
                if t.startswith("§"):
                    section, subsection = ln["head"], ""
                else:
                    subsection = ln["head"]
                paras.append(None)
                if not ln["rest"]:
                    continue
                t = ln["rest"]
                ln = {**ln, "x0": margin + 12, "math": 0.0, "font": "F41"}
            indent = ln["x0"] - margin
            display = ln["math"] > 0.6 and indent > 16
            new_par = (8 <= indent <= 16 and ln["math"] < 0.6) or (ln["font"] == "F50" and HEAD_RE.match(t))
            sec = section + (" / " + subsection if subsection else "")
            if new_par or not paras or paras[-1] is None:
                paras.append({"section": sec, "lines": [t]})
            else:
                paras[-1]["lines"].append(t)
        paras = [p for p in paras if p]
        merged = []
        for p in paras:
            text = clean(" ".join(p["lines"]))
            if merged and len(merged[-1]["text"]) < 120 and merged[-1]["section"] == p["section"]:
                merged[-1]["text"] = merged[-1]["text"] + " " + text
            else:
                merged.append({"section": p["section"], "text": text})
        for p in merged:
            for chunk in split_long(p["text"], max_chars):
                blocks.append({"page": pno + 1, "section": p["section"], "text": chunk})
    for i, b in enumerate(blocks, 1):
        b["block_id"] = f"b_{i:04d}"
    return [{"block_id": b["block_id"], "page": b["page"], "section": b["section"], "text": b["text"]} for b in blocks]


def split_long(text, max_chars):
    if len(text) <= max_chars:
        return [text]
    sents = re.split(r"(?<=[.!?□])\s+(?=[А-ЯA-Z«(])", text)
    n = -(-len(text) // max_chars)
    target = len(text) / n
    chunks, cur = [], ""
    for s in sents:
        if cur and len(cur) + len(s) > target * 1.15:
            chunks.append(cur)
            cur = s
        else:
            cur = (cur + " " + s).strip()
    if cur:
        chunks.append(cur)
    return chunks


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--pdf", required=True)
    ap.add_argument("--output", required=True)
    ap.add_argument("--max-chars", type=int, default=1100)
    args = ap.parse_args()
    blocks = build(args.pdf, args.max_chars)
    with open(args.output, "w", encoding="utf-8") as f:
        for b in blocks:
            f.write(json.dumps(b, ensure_ascii=False) + "\n")
    sha = hashlib.sha256(open(args.pdf, "rb").read()).hexdigest()
    print(f"{len(blocks)} blocks, pdf sha256 {sha}")


if __name__ == "__main__":
    main()
