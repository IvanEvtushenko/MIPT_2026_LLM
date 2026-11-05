"""Проверка формата и оценка прогона по правилам задания."""
import argparse
import csv
import json
import os
import statistics

from rag import load_jsonl

LIMITS = {"llm_calls": 4, "input_tokens": 16384, "output_tokens": 4096}


def format_errors(answers, questions, block_ids=None):
    errs = []
    qmap = {q["id"]: q for q in questions}
    ids = [a.get("id") for a in answers]
    if sorted(ids) != sorted(qmap) or len(ids) != len(set(ids)):
        errs.append(("*", "набор id не совпадает с вопросами"))
    for a in answers:
        q = qmap.get(a.get("id"))
        if q is None:
            continue
        opts = {o["id"] for o in q["options"]}
        aid, cit, ab = a.get("answer_ids"), a.get("citations"), a.get("abstain")
        if not isinstance(ab, bool) or not isinstance(aid, list) or not isinstance(cit, list):
            errs.append((a["id"], "типы полей"))
            continue
        if len(set(aid)) != len(aid) or not set(aid) <= opts:
            errs.append((a["id"], "answer_ids"))
        if ab and (aid or cit) or not ab and (not aid or not cit):
            errs.append((a["id"], "abstain и списки"))
        if not isinstance(a.get("rationale"), str) or not a["rationale"].strip() or len(a["rationale"]) > 1200:
            errs.append((a["id"], "rationale"))
        if len(cit) > 4 or len(set(cit)) != len(cit) or (block_ids is not None and not set(cit) <= block_ids):
            errs.append((a["id"], "citations"))
    return errs


def f1(pred, gold):
    tp = len(set(pred) & set(gold))
    if tp == 0:
        return 0.0
    p, r = tp / len(set(pred)), tp / len(set(gold))
    return 2 * p * r / (p + r)


def evaluate(answers, traces, questions, gold, block_ids=None, c_ref=None):
    gmap = {g["id"]: g for g in gold}
    amap = {a["id"]: a for a in answers}
    tmap = {t["id"]: t for t in traces}
    bad = {i for i, _ in format_errors(answers, questions, block_ids)}
    over = {i for i, t in tmap.items() if any(t.get(k, 0) > v for k, v in LIMITS.items())}
    rows = []
    for q in questions:
        g, a, t = gmap[q["id"]], amap.get(q["id"]), tmap.get(q["id"], {})
        correct = a is not None and a["abstain"] == g["abstain"] and set(a["answer_ids"]) == set(g["answer_ids"])
        if a is None or q["id"] in bad or q["id"] in over:
            s = 0.0
        elif g["abstain"]:
            s = float(a["abstain"])
        else:
            s = correct * (0.8 + 0.2 * max(f1(a["citations"], e) for e in g["evidence_sets"]))
        cost = t.get("input_tokens", 0) + 4 * t.get("output_tokens", 0) + 256 * t.get("llm_calls", 0)
        rows.append({"id": q["id"], "category": g["category"], "correct": correct, "s": s, "cost": cost,
                     "input": t.get("input_tokens", 0), "output": t.get("output_tokens", 0),
                     "calls": t.get("llm_calls", 0), "latency": t.get("elapsed_ms", 0)})
    cats = sorted({r["category"] for r in rows})
    by_cat = {c: statistics.mean(r["s"] for r in rows if r["category"] == c) for c in cats}
    q_score = statistics.mean(by_cat.values())
    mean_cost = statistics.mean(r["cost"] for r in rows)
    e = min(1.0, c_ref / max(mean_cost, 1)) if c_ref else None
    if over or len(tmap) < len(questions):
        e = 0.0
    return {
        "answer_accuracy": statistics.mean(r["correct"] for r in rows),
        "Q": q_score, "by_category": by_cat,
        "mean_input_tokens": statistics.mean(r["input"] for r in rows),
        "mean_output_tokens": statistics.mean(r["output"] for r in rows),
        "mean_llm_calls": statistics.mean(r["calls"] for r in rows),
        "mean_cost": mean_cost,
        "median_latency_ms": statistics.median(r["latency"] for r in rows),
        "E": e, "total": None if e is None else 75 * q_score + 25 * q_score * e,
        "format_errors": sorted(bad), "limit_violations": sorted(over), "rows": rows,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--answers", required=True)
    ap.add_argument("--trace", required=True)
    ap.add_argument("--questions", required=True)
    ap.add_argument("--gold", required=True)
    ap.add_argument("--corpus")
    ap.add_argument("--c-ref", type=float)
    ap.add_argument("--name", help="добавить строку в --csv")
    ap.add_argument("--csv", default="experiments.csv")
    ap.add_argument("--no-q", action="store_true", help="без контекста: Q не заполнять")
    args = ap.parse_args()
    block_ids = {b["block_id"] for b in load_jsonl(args.corpus)} if args.corpus else None
    res = evaluate(load_jsonl(args.answers), load_jsonl(args.trace), load_jsonl(args.questions),
                   load_jsonl(args.gold), block_ids, args.c_ref)
    for r in res["rows"]:
        print(f"{r['id']} {r['category']:12s} correct={int(r['correct'])} s={r['s']:.2f} cost={r['cost']}")
    print(json.dumps({k: v for k, v in res.items() if k != "rows"}, ensure_ascii=False, indent=1))
    if args.name:
        new = not os.path.exists(args.csv)
        with open(args.csv, "a", newline="", encoding="utf-8") as f:
            w = csv.writer(f)
            if new:
                w.writerow(["name", "answer_accuracy", "Q", "mean_input_tokens", "mean_output_tokens",
                            "mean_llm_calls", "mean_cost", "median_latency_ms"])
            w.writerow([args.name, f"{res['answer_accuracy']:.3f}", "" if args.no_q else f"{res['Q']:.3f}",
                        f"{res['mean_input_tokens']:.0f}", f"{res['mean_output_tokens']:.0f}",
                        f"{res['mean_llm_calls']:.2f}", f"{res['mean_cost']:.0f}", f"{res['median_latency_ms']:.0f}"])


if __name__ == "__main__":
    main()
