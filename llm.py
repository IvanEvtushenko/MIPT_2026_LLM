"""Клиент OpenAI-совместимого Chat Completions API, учёт лимитов, промпты и разбор JSON-ответа."""
import json
import os
import re
import time

import requests
from tokenizers import Tokenizer

from textnorm import latex_to_unicode


class BudgetExceeded(Exception):
    pass


class Budget:
    """Лимиты на вопрос: проверка до запроса, учёт по usage из ответа."""

    def __init__(self, limits, max_tokens):
        self.limits, self.max_tokens = limits, max_tokens
        self.calls = self.input = self.output = 0

    def allows(self, est_input):
        lim = self.limits
        return (self.calls + 1 <= lim["max_calls"]
                and self.input + est_input <= lim["max_input_tokens"]
                and self.output + self.max_tokens <= lim["max_output_tokens"]
                and est_input + self.max_tokens <= lim["max_request_tokens"])


class LLM:
    def __init__(self, runtime, tokenizer_path):
        self.rt = runtime
        self.base = os.environ.get("LLM_BASE_URL", runtime["access"]["dev_endpoint"]).rstrip("/")
        self.model = os.environ.get("LLM_MODEL", runtime["model"])
        self.key = os.environ.get("LLM_API_KEY", "EMPTY")
        self.submission = os.environ.get("SUBMISSION_ID", "local")
        self.tok = Tokenizer.from_file(tokenizer_path)

    def count(self, messages):
        """Оценка входных токенов с запасом на шаблон чата."""
        n = sum(len(self.tok.encode(m["content"]).ids) + 8 for m in messages) + 16
        return int(n * 1.02) + 16

    def chat(self, qid, messages, budget):
        est = self.count(messages)
        if not budget.allows(est):
            raise BudgetExceeded(f"est_input={est}")
        payload = {"model": self.model, "messages": messages, "max_tokens": self.rt["max_tokens"],
                   **self.rt["sampling"], "chat_template_kwargs": {"enable_thinking": self.rt["thinking"]}}
        headers = {"Authorization": f"Bearer {self.key}", "X-Question-ID": qid,
                   "X-Submission-ID": self.submission}
        timeout = self.rt["timeouts"]["request_seconds"]
        for attempt in range(self.rt["timeouts"]["retries"] + 1):
            budget.calls += 1
            try:
                r = requests.post(f"{self.base}/chat/completions", json=payload, headers=headers, timeout=timeout)
                r.raise_for_status()
                data = r.json()
                usage = data.get("usage") or {}
                budget.input += usage.get("prompt_tokens", est)
                budget.output += usage.get("completion_tokens", self.rt["max_tokens"])
                return data["choices"][0]["message"].get("content") or ""
            except (requests.RequestException, KeyError, ValueError):
                budget.input += est
                budget.output += self.rt["max_tokens"]
                if attempt == self.rt["timeouts"]["retries"] or not budget.allows(est):
                    raise
                time.sleep(2 * (attempt + 1))


def parse_json(text):
    text = re.sub(r"<think>.*?</think>", "", text, flags=re.S)
    m = re.search(r"\{.*\}", text, re.S)
    if not m:
        return None
    s = m.group(0)
    for cand in (s, re.sub(r",\s*([}\]])", r"\1", s)):
        try:
            return json.loads(cand)
        except json.JSONDecodeError:
            continue
    return None


FORMAT_NOTE = ("Формулы во фрагментах записаны в строку: a/b — дробь, x^{n} — степень, z_0 — индекс, "
               "z̄ — сопряжённое к z, ∫_γ — интеграл по кривой γ, f′ — производная, ℂ — комплексная плоскость.")

ANSWER_SYSTEM = """Ты отвечаешь на тестовые вопросы по теории функций комплексного переменного (ТФКП) только на основе фрагментов учебника В. В. Горяйнова и Е. С. Половинкина «Лекции по ТФКП» и коротких выводов из них.

В вопросе несколько вариантов, верных может быть несколько. Проверь каждый вариант отдельно.
- Вариант верен, если он следует из фрагментов прямо или коротким выводом: подстановкой в формулу, простым вычислением, контрпримером. Следи за точными условиями теорем.
- Если просят вычислить, вычисли сам по формулам из фрагментов (интегральная формула Коши, вычеты, ряды) и сравни с вариантами.
- Отказ (abstain=true) ставь, только если данных в условии вопроса не хватает для однозначного ответа: ответ зависит от того, что в условии не задано (значение константы, ориентация контура, сама функция), или известна лишь оценка, а спрашивают точное значение. В missing назови, чего именно не хватает.
- Если во фрагментах нет определения, теоремы или формулы, нужной для проверки, не считай вариант неверным только поэтому. Запиши в need_search, что нужно найти (название теоремы или понятия, 3–8 слов). При отказе need_search заполняй всегда.
- """ + FORMAT_NOTE + """

Верни только JSON без markdown:
{"checks": [{"id": "A", "why": "проверка варианта, до 30 слов", "ok": false}], "abstain": false, "missing": "", "need_search": "", "citations": ["b_0001"]}
В checks — по одному элементу на каждый вариант: сначала в why проверь утверждение варианта по фрагментам, затем в ok запиши итог этой проверки. citations — 1–3 block_id фрагментов, которые вместе подтверждают ответ."""

NO_CONTEXT_SYSTEM = """Ты отвечаешь на тестовые вопросы по теории функций комплексного переменного (ТФКП).
В вопросе несколько вариантов, верных может быть несколько. Проверь каждый вариант отдельно.
Отказ (abstain=true) ставь, только если данных в условии не хватает для однозначного ответа; в missing назови, чего не хватает.

Верни только JSON без markdown:
{"checks": [{"id": "A", "why": "проверка варианта, до 30 слов", "ok": false}], "abstain": false, "missing": ""}
В checks — по одному элементу на каждый вариант: сначала в why проверь утверждение варианта, затем в ok запиши итог этой проверки."""

PLAN_SYSTEM = """Ты помогаешь искать нужные места в учебнике по теории функций комплексного переменного. Задачу не решай.
Назови 2–4 поисковых запроса: какие определения, теоремы или формулы учебника нужны, чтобы проверить варианты ответа. Запрос — 3–10 слов на русском, можно с названием теоремы или формулой.
Верни только JSON: {"queries": ["...", "..."]}"""


def question_block(q):
    opts = "\n".join(f"{o['id']}. {latex_to_unicode(o['text'])}" for o in q["options"])
    return f"Вопрос: {latex_to_unicode(q['question'])}\nВарианты:\n{opts}"


def answer_messages(q, context_text):
    user = f"Фрагменты учебника:\n\n{context_text}\n\n{question_block(q)}\n\nВерни JSON."
    return [{"role": "system", "content": ANSWER_SYSTEM}, {"role": "user", "content": user}]


def no_context_messages(q):
    return [{"role": "system", "content": NO_CONTEXT_SYSTEM},
            {"role": "user", "content": question_block(q) + "\n\nВерни JSON."}]


def plan_messages(q):
    return [{"role": "system", "content": PLAN_SYSTEM},
            {"role": "user", "content": question_block(q) + "\n\nВерни JSON."}]
