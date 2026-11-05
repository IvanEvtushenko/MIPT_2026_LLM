"""Сборка индекса из corpus.jsonl. Без вызовов LLM: токены BM25, эмбеддинги, токенизатор Qwen для подсчёта."""
import argparse
import json
import os
import resource
import shutil
import time

import numpy as np
from huggingface_hub import hf_hub_download

from rag import load_jsonl
from textnorm import search_text, tokens


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--corpus", required=True)
    ap.add_argument("--output", required=True)
    ap.add_argument("--config", required=True)
    args = ap.parse_args()
    t0 = time.time()
    cfg = json.load(open(args.config, encoding="utf-8"))
    runtime = json.load(open(os.path.join(os.path.dirname(os.path.abspath(args.config)), cfg["runtime"]), encoding="utf-8"))
    os.makedirs(args.output, exist_ok=True)

    blocks = load_jsonl(args.corpus)
    ids = [b["block_id"] for b in blocks]
    assert len(ids) == len(set(ids)), "block_id не уникальны"
    blocks = [{"block_id": str(b["block_id"]), "page": b.get("page"), "section": b.get("section") or "",
               "text": b.get("text") or ""} for b in blocks]
    with open(os.path.join(args.output, "blocks.jsonl"), "w", encoding="utf-8") as f:
        for b in blocks:
            f.write(json.dumps(b, ensure_ascii=False) + "\n")

    docs = [f"{b['section']}. {b['text']}" for b in blocks]
    for variant, math in (("math", True), ("raw", False)):
        with open(os.path.join(args.output, f"tokens_{variant}.json"), "w", encoding="utf-8") as f:
            json.dump([tokens(d, math) for d in docs], f, ensure_ascii=False)

    from sentence_transformers import CrossEncoder, SentenceTransformer
    m = cfg["embedding"]
    encoder = SentenceTransformer(m["model"], revision=m["revision"], device="cpu")
    encoder.max_seq_length = m["max_length"]
    for variant, math in (("math", True), ("raw", False)):
        emb = encoder.encode([search_text(d, math) for d in docs], batch_size=m["batch_size"],
                             normalize_embeddings=True, show_progress_bar=True)
        np.save(os.path.join(args.output, f"emb_{variant}.npy"), emb.astype(np.float32))
    r = cfg["reranker"]
    CrossEncoder(r["model"], revision=r["revision"], device="cpu", max_length=r["max_length"])

    tok = hf_hub_download(runtime["model"], "tokenizer.json", revision=runtime["revision"])
    shutil.copy(tok, os.path.join(args.output, "tokenizer.json"))

    size = sum(os.path.getsize(os.path.join(args.output, f)) for f in os.listdir(args.output))
    meta = {
        "blocks": len(blocks),
        "embedding": m, "reranker": r, "llm_tokenizer": {"model": runtime["model"], "revision": runtime["revision"]},
        "build_seconds": round(time.time() - t0, 1),
        "peak_rss_mb": round(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024),
        "index_bytes": size,
    }
    with open(os.path.join(args.output, "meta.json"), "w", encoding="utf-8") as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)
    print(json.dumps(meta, ensure_ascii=False))


if __name__ == "__main__":
    main()
