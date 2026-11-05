"""Нормализация текста и формул для поиска.

LaTeX из вопросов и Unicode-формулы корпуса приводятся к одной записи (latex_to_unicode),
затем формулы превращаются в токены двух видов: слова-описания символов (∂ -> "частная
производная", z̄ -> "сопряженное") и компактные формульные токены ("∂f/∂zbar").
Без этого вопрос с $\\partial f/\\partial\\bar z$ и блок с ∂f/∂z̄ не имеют общих токенов.
"""
import re

import snowballstemmer

GREEK = {
    "alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ε", "varepsilon": "ε", "zeta": "ζ",
    "eta": "η", "theta": "θ", "vartheta": "θ", "kappa": "κ", "lambda": "λ", "mu": "µ", "nu": "ν", "xi": "ξ",
    "pi": "π", "rho": "ρ", "varrho": "ϱ", "sigma": "σ", "tau": "τ", "phi": "φ", "varphi": "φ", "chi": "χ",
    "psi": "ψ", "omega": "ω", "Gamma": "Γ", "Delta": "∆", "Theta": "Θ", "Lambda": "Λ", "Sigma": "Σ",
    "Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
}
SYMBOLS = {
    "partial": "∂", "infty": "∞", "int": "∫", "oint": "∮", "sum": "∑", "prod": "∏", "le": "⩽", "leq": "⩽",
    "leqslant": "⩽", "ge": "⩾", "geq": "⩾", "geqslant": "⩾", "ne": "≠", "neq": "≠", "to": "→",
    "rightarrow": "→", "in": "∈", "notin": "∉", "subset": "⊂", "subseteq": "⊂", "setminus": "\\", "cdot": "·",
    "times": "×", "equiv": "≡", "pm": "±", "triangle": "△", "nabla": "∇", "cap": "∩", "cup": "∪",
    "emptyset": "∅", "varnothing": "∅", "circ": "∘", "ldots": "...", "dots": "...", "cdots": "...", "sqrt": "√",
    "mid": "|", "vert": "|", "prime": "′",
}
FUNCS = {"sin", "cos", "tg", "tan", "ctg", "cot", "sh", "ch", "sinh", "cosh", "exp", "ln", "log", "Ln", "Re",
         "Im", "arg", "Arg", "lim", "max", "min", "sup", "inf", "deg", "res", "Res"}
SPACING = re.compile(r"\\(left|right|big|Big|bigg|Bigg|displaystyle|textstyle|limits|nolimits)\b|\\[,;:! ]|\\quad|\\qquad")

SYMBOL_WORDS = [
    ("∂", "частная производная"), ("∫", "интеграл"), ("∮", "интеграл контур"), ("∑", "сумма ряд"),
    ("∏", "произведение"), ("∞", "бесконечность"), ("ℂ", "комплексная плоскость"), ("ℝ", "вещественная ось"),
    ("△", "лапласиан оператор лапласа"), ("∆", "треугольник"), ("√", "корень"), ("≡", "тождественно"),
    ("→", "стремится предел"), ("′", "производная"), ("≠", "не равно"), ("∈", "принадлежит"),
]
FUNC_WORDS = {"sin": "синус", "cos": "косинус", "tg": "тангенс", "ctg": "котангенс", "sh": "синус гиперболический",
              "ch": "косинус гиперболический", "exp": "экспонента", "ln": "логарифм", "log": "логарифм",
              "Ln": "логарифм", "Re": "вещественная часть", "Im": "мнимая часть", "arg": "аргумент",
              "Arg": "аргумент", "lim": "предел", "res": "вычет", "Res": "вычет"}

STOP = set("""и в во не что он на я с со как а то все она так его но да ты к у же вы за бы по только ее мне было
вот от меня еще нет о из ему теперь когда даже ну вдруг ли если уже или ни быть был него до вас нибудь опять уж
вам ведь там потом себя ничего ей может они тут где есть надо ней для мы тебя их чем была сам чтоб без будто чего
раз тоже себе под будет ж тогда кто этот того потому этого какой совсем ним здесь этом один почти мой тем чтобы
нее сейчас были куда зачем всех никогда можно при наконец два об другой хоть после над больше тот через эти нас
про всего них какая много разве три эту моя впрочем хорошо свою этой перед иногда лучше чуть том нельзя такой им
более всегда конечно всю между это также какие которые который которая которого""".split())

_stem_ru = snowballstemmer.stemmer("russian")


def _braced(s, i):
    """Аргумент LaTeX-команды с позиции i: {...} или один символ/команда."""
    while i < len(s) and s[i] == " ":
        i += 1
    if i >= len(s):
        return "", i
    if s[i] == "{":
        depth, j = 0, i
        while j < len(s):
            depth += {"{": 1, "}": -1}.get(s[j], 0)
            if depth == 0:
                return s[i + 1:j], j + 1
            j += 1
        return s[i + 1:], len(s)
    if s[i] == "\\":
        m = re.match(r"\\[A-Za-z]+", s[i:])
        if m:
            return m.group(0), i + len(m.group(0))
    return s[i], i + 1


def _paren(s):
    s = s.strip()
    return s if re.fullmatch(r"[∂d]?\w[\u0304′]*|\d+|\w!", s) else f"({s})"


def latex_to_unicode(s):
    if "\\" not in s and "$" not in s:
        return s
    s = s.replace("$", "")
    s = SPACING.sub(" ", s)
    s = re.sub(r"\\(?:mathbb|Bbb)\s*\{?([A-Z])\}?", lambda m: {"C": "ℂ", "R": "ℝ", "Z": "ℤ", "N": "ℕ"}.get(
        m.group(1), m.group(1)), s)
    out, i = [], 0
    while i < len(s):
        if s[i] != "\\":
            out.append(s[i])
            i += 1
            continue
        m = re.match(r"\\([A-Za-z]+)", s[i:])
        if not m:
            out.append(s[i + 1:i + 2])
            i += 2
            continue
        cmd, i = m.group(1), i + len(m.group(0))
        if cmd in ("frac", "tfrac", "dfrac"):
            a, i = _braced(s, i)
            b, i = _braced(s, i)
            out.append(f"{_paren(latex_to_unicode(a))}/{_paren(latex_to_unicode(b))}")
        elif cmd in ("bar", "overline"):
            a, i = _braced(s, i)
            a = latex_to_unicode(a).strip()
            out.append(a + "\u0304" if len(a) == 1 else "\\overline{" + a + "}")
        elif cmd in ("operatorname", "mathrm", "text", "textrm", "mathit", "mathbf", "mathcal", "boldsymbol"):
            a, i = _braced(s, i)
            out.append(latex_to_unicode(a))
        elif cmd in GREEK or cmd in SYMBOLS:
            out.append(GREEK.get(cmd) or SYMBOLS[cmd])
            if s[i:i + 1] == " " and s[i + 1:i + 2].isalnum():
                i += 1
        elif cmd in FUNCS:
            out.append(cmd + " ")
        else:
            out.append(cmd)
    return re.sub(r"[ \t]+", " ", "".join(out)).strip()


def math_words(text):
    """Слова, описывающие символы формул: добавляются к тексту только для поиска."""
    words = [w for sym, w in SYMBOL_WORDS if sym in text]
    if "\u0304" in text or "\\overline" in text:
        words.append("сопряженное черта")
    if re.search(r"\w!", text):
        words.append("факториал")
    if re.search(r"\be\^", text):
        words.append("экспонента")
    if re.search(r"\^\{?\(\w+\)", text):
        words.append("производная порядка")
    for f in re.findall(r"(?<![A-Za-z])(sin|cos|tg|ctg|sh|ch|exp|ln|log|Ln|Re|Im|arg|Arg|lim|res|Res)(?![A-Za-z])", text):
        words.append(FUNC_WORDS[f])
    return " ".join(dict.fromkeys(words))


def formula_tokens(text):
    """Компактные формульные токены: последовательности без кириллицы и пробелов, без скобок."""
    toks = []
    for chunk in re.findall(r"[^\sА-Яа-яЁё]+", text):
        chunk = chunk.strip(".,;:")
        for part in re.split(r"[=+−\-,<>⩽⩾·]", chunk):
            part = re.sub(r"[(){}\[\]\s]", "", part).replace("\u0304", "bar").lower()
            if len(part) >= 2 and re.search(r"[^\d.]", part):
                toks.append("§" + part)
    return toks


def word_tokens(text):
    toks = []
    for w in re.findall(r"[A-Za-zА-Яа-яЁё]+", text.lower().replace("ё", "е")):
        if w in STOP or len(w) < 2:
            continue
        toks.append(_stem_ru.stemWord(w) if re.match(r"[а-я]", w) else w)
    return toks


def search_text(text, math=True):
    """Текст для dense-поиска: при math=True LaTeX -> Unicode и слова-описания формул."""
    if not math:
        return text
    t = latex_to_unicode(text)
    mw = math_words(t)
    return f"{t} ({mw})" if mw else t


def tokens(text, math=True):
    """Токены для BM25. math=False: только слова исходного текста (абляция)."""
    if not math:
        return word_tokens(text)
    t = latex_to_unicode(text)
    return word_tokens(t) + word_tokens(math_words(t)) + formula_tokens(t)
