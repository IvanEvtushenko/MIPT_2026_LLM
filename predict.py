"""Ответы на вопросы: questions.jsonl -> answers.jsonl и trace.jsonl.

Режимы (config.mode): no_context, rag_simple (один поиск, один вызов), final
(поиск -> ответ -> при нехватке фрагментов повторный поиск и второй ответ; планировщик запросов опционален).
"""
import argparse
import json
import os
import re
import time

import requests

from llm import LLM, Budget, BudgetExceeded, answer_messages, no_context_messages, parse_json, plan_messages
from rag import Index, build_context, fmt_block, load_jsonl


def option_ids(q):
    return [str(o["id"]) for o in q["options"]]


MISSING_RE = re.compile(r"(в|во) фрагмент\w* (нет|не )|нет (в|во) фрагмент|фрагмент\w* не (содерж|привод|указ|да)|"
                        r"не (приведен|указан|найден|описан)\w* (в|во) фрагмент", re.I)


def missing_options(res, q):
    """Варианты, отвергнутые с пояснением, что во фрагментах нет нужного: повод для повторного поиска."""
    valid = option_ids(q)
    return [str(c.get("id")).strip() for c in res.get("checks") or []
            if isinstance(c, dict) and c.get("ok") is not True and str(c.get("id", "")).strip() in valid
            and MISSING_RE.search(str(c.get("why", "")))]


def checked_ids(res, q):
    valid = option_ids(q)
    ok = []
    for c in res.get("checks") or []:
        if isinstance(c, dict) and c.get("ok") is True and str(c.get("id", "")).strip() in valid:
            ok.append(str(c["id"]).strip())
    if not res.get("checks") and isinstance(res.get("answer_ids"), list):
        ok = [str(a) for a in res["answer_ids"] if str(a) in valid]
    return [i for i in valid if i in ok]


def make_rationale(res, q, abstain, limit):
    if abstain:
        reason = str(res.get("missing") or "").strip() if res else ""
        text = "Отказ: " + (reason or "условие и фрагменты не определяют ответ однозначно.")
    else:
        why = {str(c.get("id", "")).strip(): str(c.get("why", "")).strip()
               for c in res.get("checks") or [] if isinstance(c, dict)}
        ok = set(checked_ids(res, q))
        parts = [f"{i} — {'верно' if i in ok else 'неверно'}: {why[i]}".rstrip(": ") for i in option_ids(q) if i in why]
        text = "; ".join(parts) or "Варианты проверены по фрагментам."
    if len(text) > limit:
        text = text[:limit - 1].rsplit(" ", 1)[0] + "…"
    return text


def finalize(q, res, shown, ranked_shown, cfg):
    abstain = res is None or bool(res.get("abstain")) or not checked_ids(res, q)
    out = {"id": q["id"], "answer_ids": [], "abstain": abstain,
           "rationale": make_rationale(res, q, abstain, cfg["rationale_max_chars"]), "citations": []}
    if abstain:
        return out
    out["answer_ids"] = checked_ids(res, q)
    cits = []
    for c in res.get("citations") or []:
        c = str(c).strip().strip("[]")
        if c in shown and c not in cits:
            cits.append(c)
    out["citations"] = cits[:cfg["citations_max"]] or ranked_shown[:1]
    return out


def run_question(q, index, llm, cfg, rt):
    t0 = time.time()
    budget = Budget(rt["limits"], rt["max_tokens"])
    rc, mode = cfg["retrieval"], cfg["mode"]
    shown, ranked_shown, decisions, res = [], [], [], None
    count = lambda text: len(llm.tok.encode(text).ids)

    def answer(ranked):
        ctx = build_context(index, ranked, rc, count)
        for i in ranked:
            bid = index.blocks[i]["block_id"]
            if i in ctx and bid not in ranked_shown:
                ranked_shown.append(bid)
        for i in ctx:
            if index.blocks[i]["block_id"] not in shown:
                shown.append(index.blocks[i]["block_id"])
        text = "\n\n".join(fmt_block(index.blocks[i]) for i in ctx)
        return parse_json(llm.chat(q["id"], answer_messages(q, text), budget)), ranked

    try:
        if mode == "no_context":
            res = parse_json(llm.chat(q["id"], no_context_messages(q), budget))
        else:
            qs = index.queries(q["question"], q["options"], rc["per_option_queries"])
            if cfg.get("planner"):
                plan = parse_json(llm.chat(q["id"], plan_messages(q), budget)) or {}
                extra = [s for s in plan.get("queries") or [] if isinstance(s, str) and s.strip()][:4]
                decisions.append({"step": "plan", "queries": extra})
                qs += extra
            res, ranked = answer(index.search(qs, rc))
            if mode == "final" and cfg["adaptive"]["enabled"]:
                need = str((res or {}).get("need_search") or "").strip()
                unresolved = res is None or bool(res.get("abstain")) or not checked_ids(res, q)
                lacking = missing_options(res, q) if res else []
                if res is None:
                    action, why = "retry", "ответ не разобран как JSON"
                elif need:
                    action, why = "research", f"во фрагментах не хватает: {need}"
                elif lacking:
                    action, why = "research", f"для вариантов {', '.join(lacking)} модель не нашла опоры во фрагментах"
                else:
                    action, why = "stop", "ответ получен" if not unresolved else "отказ по условию вопроса"
                decisions.append({"step": "after_answer", "action": action, "reason": why})
                if action == "research":
                    texts = {o["id"]: o["text"] for o in q["options"]}
                    new = index.search([need] if need else [q["question"] + "\n" + texts[i] for i in lacking], rc)
                    fresh = [i for i in new if index.blocks[i]["block_id"] not in shown][:cfg["adaptive"]["retry_blocks"]]
                    res, _ = answer(fresh + [i for i in ranked if i not in fresh])
                elif action == "retry":
                    res, _ = answer(ranked)
    except (BudgetExceeded, requests.RequestException) as e:
        decisions.append({"step": "error", "reason": f"{type(e).__name__}: {e}"[:300]})
    ans = finalize(q, res, shown, ranked_shown, cfg)
    trace = {"id": q["id"], "llm_calls": budget.calls, "input_tokens": budget.input,
             "output_tokens": budget.output, "elapsed_ms": int((time.time() - t0) * 1000),
             "retrieved_block_ids": shown, "decisions": decisions}
    return ans, trace


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--index", required=True)
    ap.add_argument("--questions", required=True)
    ap.add_argument("--output", required=True)
    ap.add_argument("--trace", required=True)
    ap.add_argument("--config", required=True)
    args = ap.parse_args()
    cfg = json.load(open(args.config, encoding="utf-8"))
    rt_path = os.path.join(os.path.dirname(os.path.abspath(args.config)), cfg["runtime"])
    rt = json.load(open(rt_path, encoding="utf-8"))
    index = Index(args.index, cfg)
    llm = LLM(rt, os.path.join(args.index, "tokenizer.json"))
    questions, seen = [], set()
    for q in load_jsonl(args.questions):
        if q["id"] not in seen:
            seen.add(q["id"])
            questions.append(q)
    with open(args.output, "w", encoding="utf-8") as fa, open(args.trace, "w", encoding="utf-8") as ft:
        for q in questions:
            ans, trace = run_question(q, index, llm, cfg, rt)
            fa.write(json.dumps(ans, ensure_ascii=False) + "\n")
            ft.write(json.dumps(trace, ensure_ascii=False) + "\n")
            fa.flush()
            ft.flush()
            print(f"{q['id']}: {ans['answer_ids'] or 'abstain'} calls={trace['llm_calls']} "
                  f"in={trace['input_tokens']} out={trace['output_tokens']} {trace['elapsed_ms']}ms", flush=True)


if __name__ == "__main__":
    main()
