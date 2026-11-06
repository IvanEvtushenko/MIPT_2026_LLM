"""tfkp_15_qa.md -> dev_questions.jsonl, dev_gold.jsonl.

Допустимые наборы источников заданы якорями (страница, начало текста блока)
и разрешаются в block_id по корпусу, поэтому переживают пересборку корпуса.
"""
import argparse
import json
import re

CATEGORIES = {"условия": "conditions", "сопоставление": "comparison", "применение": "computation",
              "недостаточность данных": "insufficient"}

T21, D22 = (11, "Теорема 2.1."), (11, "Определение 2.2.")
DZ = (12, "Наглядный способ")
LAPL, D23 = (13, "Система уравнений Коши"), (13, "Определение 2.3.")
T41, CIRCLE, R41 = (26, "Теорема 4.1."), (28, "Наконец, рассмотрим"), (31, "Замечание 4.1.")
T44 = (30, "Теорема 4.4.")
T51, T51B, C51 = (31, "Теорема 5.1"), (32, "является голоморфной в ℂ"), (33, "Следствие 5.1.")
T53, EXP, EXP2 = (34, "Теорема 5.3."), (20, "Экспонента."), (20, "(3.3)")
MORERA = (35, "Следующий результат по формулировке")
T61, R61 = (36, "Теорема 6.1."), (37, "Замечание 6.1.")
T62, LIOUV, ENTIRE = (37, "Следующий результат показывает"), (37, "Следствие 6.1"), (37, "К целым функциям")
T63, TRIG = (38, "Теорема 6.3"), (21, "Тригонометрические функции.")
ORIENT = (26, "Аддитивность.")

EVIDENCE = {
    1: [[T21], [T21, D22]],
    2: [[DZ]],
    3: [[T21, D23], [D23], [LAPL, D23]],
    4: [[CIRCLE, R41], [T41, CIRCLE, R41]],
    5: [[T44]],
    6: [[T51, C51], [T51, T51B, C51], [C51]],
    7: [[T53], [T53, EXP], [T53, EXP2]],
    8: [[MORERA]],
    9: [[LAPL, T53], [T21, LAPL, T53], [LAPL, D23, T53]],
    10: [[T61], [T61, R61]],
    11: [[T62]],
    12: [[T63], [T63, ENTIRE], [T63, TRIG]],
    13: [[LIOUV]],
    14: [[ORIENT, CIRCLE]],
    15: [[T61]],
}


def resolve(anchor, corpus):
    page, prefix = anchor
    hits = [b["block_id"] for b in corpus if b["page"] == page and b["text"].startswith(prefix)]
    if len(hits) != 1:
        hits = [b["block_id"] for b in corpus if b["page"] == page and prefix in b["text"]]
    assert len(hits) == 1, (anchor, hits)
    return hits[0]


def parse(md):
    items = []
    for part in re.split(r"\n## (?=\d+\. )", md)[1:]:
        num = int(part.split(".", 1)[0])
        qtype = re.search(r"\*\*Тип:\*\* ([^.]+)\.", part).group(1)
        question = re.search(r"\*\*Вопрос\.\*\* (.+?)\n\n", part, re.S).group(1).strip()
        options = [{"id": m.group(1), "text": m.group(2).strip()}
                   for m in re.finditer(r"^- \*\*([A-F])\.\*\* (.+)$", part, re.M)]
        answer = re.search(r"\*\*Ответ:\*\* (.+?)\n", part).group(1).strip().rstrip(".")
        rationale = re.search(r"\*\*Ответ:\*\*.+?\n\n(.+?)\n\n", part, re.S).group(1).strip()
        abstain = answer.startswith("Отказ")
        items.append({
            "num": num, "category": CATEGORIES[qtype], "question": question, "options": options,
            "answer_ids": [] if abstain else [a.strip() for a in answer.split(",")],
            "abstain": abstain, "rationale": rationale,
        })
    return items


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--qa", required=True)
    ap.add_argument("--corpus", required=True)
    ap.add_argument("--out-dir", default="data")
    args = ap.parse_args()
    corpus = [json.loads(line) for line in open(args.corpus, encoding="utf-8")]
    items = parse(open(args.qa, encoding="utf-8").read())
    with open(f"{args.out_dir}/dev_questions.jsonl", "w", encoding="utf-8") as fq, \
            open(f"{args.out_dir}/dev_gold.jsonl", "w", encoding="utf-8") as fg:
        for it in items:
            qid = f"q_{it['num']:03d}"
            sets = [sorted({resolve(a, corpus) for a in s}) for s in EVIDENCE[it["num"]]]
            fq.write(json.dumps({"id": qid, "question": it["question"], "options": it["options"]},
                                ensure_ascii=False) + "\n")
            fg.write(json.dumps({"id": qid, "category": it["category"], "answer_ids": it["answer_ids"],
                                 "abstain": it["abstain"], "evidence_sets": sets, "rationale": it["rationale"]},
                                ensure_ascii=False) + "\n")
    print(f"{len(items)} questions")


if __name__ == "__main__":
    main()
