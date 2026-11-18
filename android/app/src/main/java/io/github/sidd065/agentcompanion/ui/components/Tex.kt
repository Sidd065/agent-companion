package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.BaselineShift
import androidx.compose.ui.unit.em

/**
 * Renders TeX math (what agents write between `$…$`) as readable text: symbols
 * and Greek letters as Unicode, sub- and superscripts as shifted smaller text,
 * \frac as a/b, \sqrt as √, \text{…} as its words. Not a typesetter: layout
 * stays on one line, unknown commands show their name.
 */
object Tex {
    fun append(out: AnnotatedString.Builder, tex: String) = Renderer(tex, out, Last()).run()

    /** The text [append] produces, without styles. */
    fun plain(tex: String): String = AnnotatedString.Builder().also { append(it, tex) }.toAnnotatedString().text

    private val sub = SpanStyle(baselineShift = BaselineShift.Subscript, fontSize = 0.75.em)
    private val sup = SpanStyle(baselineShift = BaselineShift.Superscript, fontSize = 0.75.em)
    private val bold = SpanStyle(fontWeight = FontWeight.Bold)
    private val mono = SpanStyle(fontFamily = FontFamily.Monospace)

    private val symbols = mapOf(
        "times" to "×", "cdot" to "·", "div" to "÷", "pm" to "±", "mp" to "∓", "ast" to "∗", "star" to "⋆", "circ" to "∘",
        "bullet" to "•", "le" to "≤", "leq" to "≤", "leqslant" to "≤", "ge" to "≥", "geq" to "≥", "geqslant" to "≥",
        "ne" to "≠", "neq" to "≠", "approx" to "≈", "equiv" to "≡", "sim" to "∼", "simeq" to "≃", "cong" to "≅",
        "propto" to "∝", "ll" to "≪", "gg" to "≫", "coloneqq" to "≔", "triangleq" to "≜", "infty" to "∞",
        "to" to "→", "rightarrow" to "→", "leftarrow" to "←", "gets" to "←", "Rightarrow" to "⇒", "Leftarrow" to "⇐",
        "leftrightarrow" to "↔", "Leftrightarrow" to "⇔", "iff" to "⟺", "implies" to "⟹", "mapsto" to "↦",
        "uparrow" to "↑", "downarrow" to "↓", "longrightarrow" to "⟶", "longmapsto" to "⟼",
        "in" to "∈", "notin" to "∉", "ni" to "∋", "subset" to "⊂", "subseteq" to "⊆", "supset" to "⊃", "supseteq" to "⊇",
        "cup" to "∪", "cap" to "∩", "setminus" to "∖", "emptyset" to "∅", "varnothing" to "∅", "forall" to "∀",
        "exists" to "∃", "nexists" to "∄", "neg" to "¬", "lnot" to "¬", "land" to "∧", "wedge" to "∧", "lor" to "∨",
        "vee" to "∨", "oplus" to "⊕", "otimes" to "⊗", "partial" to "∂", "nabla" to "∇", "sum" to "∑", "prod" to "∏",
        "coprod" to "∐", "int" to "∫", "iint" to "∬", "oint" to "∮", "cdots" to "⋯", "ldots" to "…", "dots" to "…",
        "dotsc" to "…", "dotsb" to "⋯", "vdots" to "⋮", "ddots" to "⋱", "prime" to "′", "langle" to "⟨", "rangle" to "⟩",
        "lfloor" to "⌊", "rfloor" to "⌋", "lceil" to "⌈", "rceil" to "⌉", "mid" to "∣", "vert" to "|", "lvert" to "|",
        "rvert" to "|", "Vert" to "‖", "lVert" to "‖", "rVert" to "‖", "parallel" to "∥", "perp" to "⊥", "angle" to "∠",
        "degree" to "°", "ell" to "ℓ", "hbar" to "ℏ", "Re" to "ℜ", "Im" to "ℑ", "aleph" to "ℵ", "therefore" to "∴",
        "because" to "∵", "checkmark" to "✓", "dagger" to "†", "lbrace" to "{", "rbrace" to "}", "backslash" to "\\",
        "colon" to ":", "top" to "⊤", "bot" to "⊥", "models" to "⊨", "vdash" to "⊢", "square" to "□", "Box" to "□",
        "alpha" to "α", "beta" to "β", "gamma" to "γ", "delta" to "δ", "epsilon" to "ϵ", "varepsilon" to "ε",
        "zeta" to "ζ", "eta" to "η", "theta" to "θ", "vartheta" to "ϑ", "iota" to "ι", "kappa" to "κ", "lambda" to "λ",
        "mu" to "μ", "nu" to "ν", "xi" to "ξ", "pi" to "π", "varpi" to "ϖ", "rho" to "ρ", "varrho" to "ϱ",
        "sigma" to "σ", "varsigma" to "ς", "tau" to "τ", "upsilon" to "υ", "phi" to "ϕ", "varphi" to "φ", "chi" to "χ",
        "psi" to "ψ", "omega" to "ω", "Gamma" to "Γ", "Delta" to "Δ", "Theta" to "Θ", "Lambda" to "Λ", "Xi" to "Ξ",
        "Pi" to "Π", "Sigma" to "Σ", "Upsilon" to "Υ", "Phi" to "Φ", "Psi" to "Ψ", "Omega" to "Ω",
        // Spacing.
        "quad" to "  ", "qquad" to "    ", "enspace" to " ", "thinspace" to " ",
    )
    private val functions = setOf(
        "min", "max", "log", "ln", "lg", "exp", "sin", "cos", "tan", "sec", "csc", "cot", "arcsin", "arccos", "arctan",
        "sinh", "cosh", "tanh", "lim", "liminf", "limsup", "sup", "inf", "arg", "argmin", "argmax", "det", "deg", "dim",
        "gcd", "lcm", "ker", "hom", "Pr", "mod", "bmod", "E", "Var", "Cov",
    )
    /** Commands whose one argument is shown as is (with an optional style). */
    private val textual = setOf(
        "text", "textrm", "textnormal", "textup", "textit", "textsf", "emph", "mbox", "hbox", "mathrm", "mathit",
        "mathsf", "mathcal", "mathscr", "mathfrak", "operatorname", "operatorname*", "mathnormal", "boldsymbol", "bm",
        "mathbf", "textbf", "texttt", "mathtt", "displaystyle",
    )
    private val ignored = setOf(
        "left", "right", "middle", "big", "Big", "bigg", "Bigg", "bigl", "bigr", "Bigl", "Bigr", "biggl", "biggr",
        "Biggl", "Biggr", "displaystyle", "textstyle", "scriptstyle", "limits", "nolimits", "nonumber", "notag", "label",
        "tag", "phantom", "hphantom", "vphantom", "mathstrut", "strut", "allowbreak", "relax", "not",
    )
    private val accents = mapOf(
        "hat" to '̂', "widehat" to '̂', "bar" to '̄', "overline" to '̅', "underline" to '̲',
        "vec" to '⃗', "overrightarrow" to '⃗', "tilde" to '̃', "widetilde" to '̃', "dot" to '̇',
        "ddot" to '̈', "acute" to '́', "grave" to '̀', "breve" to '̆', "check" to '̌',
    )
    private val textEscape = Regex("\\\\([_%$&#{} ])")
    private val doubleStruck = mapOf(
        'N' to "ℕ", 'Z' to "ℤ", 'Q' to "ℚ", 'R' to "ℝ", 'C' to "ℂ", 'P' to "ℙ", 'H' to "ℍ", 'E' to "𝔼", '1' to "𝟙",
    )

    /** The last character this math run wrote (null at its start). */
    private class Last(var c: Char? = null)

    private class Renderer(val s: String, val out: AnnotatedString.Builder, val last: Last, val depth: Int = 0) {
        var i = 0

        fun run() {
            // Absurdly nested input stays text instead of exhausting the stack.
            if (depth > 24) { put(s); return }
            while (i < s.length) atom()
        }

        private fun put(c: Char) { out.append(c); last.c = c }
        private fun put(t: String) { if (t.isNotEmpty()) { out.append(t); last.c = t.last() } }

        private fun space() {
            val c = last.c
            if (c != null && c != ' ' && c != '\n' && c != '(' && c != '[') put(' ')
        }

        /** Renders [tex] (a group's contents) in place. */
        private fun inner(tex: String) = Renderer(tex, out, last, depth + 1).run()

        private fun styled(style: SpanStyle, tex: String) {
            val at = out.pushStyle(style)
            inner(tex)
            out.pop(at)
        }

        private fun atom() {
            val c = s[i]
            when {
                c == '\\' -> command()
                c == '{' -> inner(group())
                c == '}' -> i++
                c == '_' -> { i++; styled(sub, arg()) }
                c == '^' -> { i++; styled(sup, arg()) }
                c == '&' -> { i++; space() }
                c == '~' -> { i++; put(' ') }
                c == '\'' -> { i++; put('′') }
                c.isWhitespace() -> {
                    while (i < s.length && s[i].isWhitespace()) i++
                    space()
                }
                else -> { put(c); i++ }
            }
        }

        /** Reads a {…} group starting at i (on '{') and returns its contents. */
        private fun group(): String {
            var depth = 0
            val start = i + 1
            while (i < s.length) {
                when (s[i]) {
                    '\\' -> i++
                    '{' -> depth++
                    '}' -> if (--depth == 0) { i++; return s.substring(start, i - 1) }
                }
                i++
            }
            return s.substring(minOf(start, s.length))
        }

        /** The TeX of the next argument: a group's contents, a command with its groups, or one character. */
        private fun arg(): String {
            while (i < s.length && s[i] == ' ') i++
            if (i >= s.length) return ""
            return when (s[i]) {
                '{' -> group()
                '\\' -> {
                    val start = i
                    i++
                    if (i < s.length && s[i].isLetter()) while (i < s.length && s[i].isLetter()) i++ else i++
                    val name = s.substring(start + 1, i)
                    val n = when {
                        name in textual || name in accents || name == "mathbb" || name == "sqrt" -> 1
                        name.endsWith("frac") || name == "binom" -> 2
                        else -> 0
                    }
                    repeat(n) {
                        while (i < s.length && s[i] == ' ') i++
                        if (i < s.length && s[i] == '{') group()
                    }
                    s.substring(start, i)
                }
                else -> s[i++].toString()
            }
        }

        private fun simple(tex: String) = tex.trim().let { t -> t.length <= 1 || t.all { it.isLetterOrDigit() || it == '.' } }

        private fun command() {
            i++ // the backslash
            if (i >= s.length) return
            val c = s[i]
            if (!c.isLetter()) {
                i++
                when (c) {
                    '\\' -> put('\n')
                    ',', ':', '>', ' ' -> put(' ')
                    ';' -> space()
                    '!' -> {}
                    else -> put(c) // \{ \} \% \$ \& \# \_ \| …
                }
                return
            }
            val start = i
            while (i < s.length && s[i].isLetter()) i++
            var name = s.substring(start, i)
            if (name == "operatorname" && i < s.length && s[i] == '*') { i++; name = "operatorname*" }
            when {
                name in symbols -> {
                    val sym = symbols.getValue(name)
                    // Relations and arrows read better with room around them.
                    if (sym.length == 1 && sym[0] in "≤≥≠≈≡∼≃≅∝≪≫≔≜→←⇒⇐↔⇔⟺⟹↦∈∉∋⊂⊆⊃⊇⟶⟼") {
                        space(); put(sym); put(' ')
                        while (i < s.length && s[i] == ' ') i++
                    } else put(sym)
                }
                name in functions -> put(name)
                name == "pmod" -> { space(); put("(mod "); inner(arg()); put(')') }
                name in textual -> {
                    val a = arg()
                    when (name) {
                        "mathbf", "textbf", "boldsymbol", "bm" -> styled(bold, a)
                        "texttt", "mathtt" -> styled(mono, a)
                        "text", "textrm", "textnormal", "textup", "textit", "textsf", "emph", "mbox", "hbox" ->
                            put(a.replace(textEscape, "$1").replace('~', ' '))
                        else -> inner(a)
                    }
                }
                name == "mathbb" -> arg().forEach { ch -> put(doubleStruck[ch] ?: ch.toString()) }
                name.endsWith("frac") || name == "binom" -> {
                    val num = arg()
                    val den = arg()
                    if (name == "binom") { put("C("); inner(num); put(", "); inner(den); put(')'); return }
                    if (simple(num)) inner(num) else { put('('); inner(num); put(')') }
                    put('/')
                    if (simple(den)) inner(den) else { put('('); inner(den); put(')') }
                }
                name == "sqrt" -> {
                    while (i < s.length && s[i] == ' ') i++
                    if (i < s.length && s[i] == '[') {
                        val end = s.indexOf(']', i)
                        if (end > i) { styled(sup, s.substring(i + 1, end)); i = end + 1 }
                    }
                    val a = arg()
                    put('√')
                    if (simple(a)) inner(a) else { put('('); inner(a); put(')') }
                }
                name in accents -> {
                    val mark = accents.getValue(name)
                    val text = AnnotatedString.Builder().also { Renderer(arg(), it, Last(), depth + 1).run() }.toAnnotatedString().text
                    text.forEach { ch -> put(ch); if (!ch.isWhitespace()) put(mark) }
                }
                name == "begin" || name == "end" -> { while (i < s.length && s[i] == ' ') i++; if (i < s.length && s[i] == '{') group() }
                name in ignored -> {
                    // \left. and \right. are invisible delimiters.
                    if ((name == "left" || name == "right") && i < s.length && s[i] == '.') i++
                }
                else -> put(name)
            }
        }
    }
}
