"""Поиск по индексу: BM25 + dense (RRF), реранкер, соседние блоки, сборка контекста."""
import json
import math
import os
import re
from collections import Counter

import numpy as np

from textnorm import latex_to_unicode, search_text, tokens


def load_jsonl(path):
    with open(path, encoding="utf-8") as f:
        return [json.loads(line) for line in f if line.strip()]


REF_KINDS = {"теорем": "thm", "следстви": "cor", "лемм": "lem", "определени": "def", "замечани": "rem"}


def label_refs(blocks):
    """Метки теорем и формул: где определены и где на них ссылаются.
    Определение — "Теорема 5.3." в тексте блока или первое появление номера формулы "(5.2)"."""
    defs, uses = {}, []
    for i, b in enumerate(blocks):
        t = b["text"]
        for m in re.finditer(r"(Теорем|Следстви|Лемм|Определени|Замечани)[аеие] (\d+\.\d+)[.( ]", t):
            defs.setdefault(REF_KINDS[m.group(1).lower()] + ":" + m.group(2), i)
        for m in re.finditer(r"\((\d+\.\d+)\)", t):
            defs.setdefault("eq:" + m.group(1), i)
    for i, b in enumerate(blocks):
        t = b["text"]
        found = []
        for m in re.finditer(r"\b(теорем|следстви|лемм|определени|замечани)\w*\s+(\d+\.\d+)", t.lower()):
            found.append(REF_KINDS[m.group(1)] + ":" + m.group(2))
        found += ["eq:" + n for n in re.findall(r"\((\d+\.\d+)\)", t)]
        uses.append([lab for lab in dict.fromkeys(found) if defs.get(lab, i) != i])
    return defs, uses


class BM25:
    def __init__(self, docs, k1=1.5, b=0.75):
        self.k1, self.b = k1, b
        self.tf = [Counter(d) for d in docs]
        self.len = np.array([len(d) for d in docs], dtype=float)
        self.avg = self.len.mean() if len(docs) else 1.0
        df = Counter(t for d in self.tf for t in d)
        n = len(docs)
        self.idf = {t: math.log(1 + (n - c + 0.5) / (c + 0.5)) for t, c in df.items()}

    def scores(self, query):
        s = np.zeros(len(self.tf))
        norm = self.k1 * (1 - self.b + self.b * self.len / self.avg)
        for t in set(query):
            idf = self.idf.get(t)
            if idf is None:
                continue
            f = np.array([d.get(t, 0) for d in self.tf], dtype=float)
            s += idf * f * (self.k1 + 1) / (f + norm)
        return s


def hf_kwargs(model_cfg):
    return {"revision": model_cfg["revision"], "device": "cpu"}


class Index:
    def __init__(self, path, cfg):
        self.cfg = cfg
        self.blocks = load_jsonl(os.path.join(path, "blocks.jsonl"))
        self.pos = {b["block_id"]: i for i, b in enumerate(self.blocks)}
        self.bm25 = {}
        self.emb = {}
        for variant in ("math", "raw"):
            with open(os.path.join(path, f"tokens_{variant}.json"), encoding="utf-8") as f:
                self.bm25[variant] = BM25(json.load(f))
            p = os.path.join(path, f"emb_{variant}.npy")
            if os.path.exists(p):
                self.emb[variant] = np.load(p)
        self.defs, self.uses = label_refs(self.blocks)
        self._encoder = None
        self._reranker = None

    @property
    def encoder(self):
        if self._encoder is None:
            from sentence_transformers import SentenceTransformer
            m = self.cfg["embedding"]
            self._encoder = SentenceTransformer(m["model"], **hf_kwargs(m))
            self._encoder.max_seq_length = m["max_length"]
        return self._encoder

    @property
    def reranker(self):
        if self._reranker is None:
            from sentence_transformers import CrossEncoder
            m = self.cfg["reranker"]
            self._reranker = CrossEncoder(m["model"], max_length=m["max_length"], **hf_kwargs(m))
        return self._reranker

    def queries(self, question, options, per_option):
        full = question + "\n" + "\n".join(o["text"] for o in options)
        qs = [full]
        if per_option:
            qs += [question + "\n" + o["text"] for o in options]
        return qs

    def search(self, qs, rc):
        """qs[0] — основной запрос, остальные — дополнительные. Возвращает индексы блоков, лучшие первыми."""
        variant = "math" if rc["math_normalization"] else "raw"
        math_on = rc["math_normalization"]
        fused = np.zeros(len(self.blocks))
        for q in qs:
            ranked = [np.argsort(-self.bm25[variant].scores(tokens(q, math_on)))[:rc["bm25_k"]]]
            if rc["dense"]:
                qv = self.encoder.encode([search_text(q, math_on)], normalize_embeddings=True)[0]
                ranked.append(np.argsort(-(self.emb[variant] @ qv))[:rc["dense_k"]])
            for lst in ranked:
                for r, i in enumerate(lst):
                    fused[i] += 1.0 / (rc["rrf_k"] + r + 1)
        cand = [int(i) for i in np.argsort(-fused) if fused[i] > 0][:rc["rerank_top"]]
        if rc["rerank"] and cand:
            q = search_text(qs[0], math_on) if math_on else qs[0]
            pairs = [(q, self.passage(i, math_on)) for i in cand]
            sc = self.reranker.predict(pairs, batch_size=8, show_progress_bar=False)
            cand = [cand[j] for j in np.argsort(-np.asarray(sc))]
        return cand

    def passage(self, i, math_on=True):
        b = self.blocks[i]
        return search_text(f"{b['section']}. {b['text']}", math_on)

    def references(self, i, limit=2):
        """Блоки, где определены теоремы и формулы, на которые ссылается блок i."""
        return list(dict.fromkeys(self.defs[lab] for lab in self.uses[i]))[:limit]

    def neighbors(self, i):
        """Соседние блоки той же главы, если текст разорван: продолжение со строчной буквы
        или блок оборван без точки."""
        out = []
        b = self.blocks[i]
        if i > 0 and re.match(r"[a-zа-яё(]", b["text"]) and self.blocks[i - 1]["section"] == b["section"]:
            out.append(i - 1)
        if i + 1 < len(self.blocks) and self.blocks[i + 1]["section"] == b["section"]:
            nxt = self.blocks[i + 1]["text"]
            if not re.search(r"[.□]\s*(\(\d+\.\d+\))?$", b["text"]) or re.match(r"[a-zа-яё]", nxt):
                out.append(i + 1)
        return out


def build_context(index, ranked, rc, count_tokens):
    """Лучшие блоки до лимита блоков и токенов; у первых neighbor_top добавляются соседи.
    Блоки упорядочиваются по тексту книги."""
    chosen, total, primary = [], 0, 0
    for rank, i in enumerate(ranked):
        if primary >= rc["context_blocks"]:
            break
        if i in chosen:
            primary += 1
            continue
        group = [i]
        if rc["neighbors"] and rank < rc["neighbor_top"]:
            group += [j for j in index.neighbors(i) if j not in chosen]
        if rc.get("references") and rank < rc["neighbor_top"]:
            group += [j for j in index.references(i) if j not in chosen and j not in group]
        cost = sum(count_tokens(fmt_block(index.blocks[j])) for j in group)
        if total + cost > rc["context_tokens"]:
            continue
        chosen += group
        total += cost
        primary += 1
    return sorted(chosen)


def fmt_block(b):
    return f"[{b['block_id']}] (с. {b['page']}, {b['section']})\n{b['text']}"


def question_text(q):
    return latex_to_unicode(q["question"])
