package io.github.sidd065.agentcompanion.ui.components

private val fence = Regex("""(?m)^[ \t]*(```|~~~).*(\n|$)""")
private val tableRule = Regex("""(?m)^[ \t]*\|?[ \t]*:?-{3,}[-:| \t]*$""")
private val rule = Regex("""(?m)^[ \t]*([-*_])([ \t]*\1){2,}[ \t]*$""")
private val image = Regex("""!\[([^\]]*)]\([^)]*\)""")
private val link = Regex("""\[([^\]]+)]\([^)]*\)""")
private val autolink = Regex("""<((?:https?|file)://[^>\s]+)>""")
private val heading = Regex("""(?m)^[ \t]{0,3}#{1,6}[ \t]+""")
private val quote = Regex("""(?m)^[ \t]{0,3}>[ \t]?""")
private val bullet = Regex("""(?m)^([ \t]*)[-*+][ \t]+(\[[ xX]][ \t]+)?""")
private val codeSpan = Regex("""(`+)(.+?)\1""")
// Emphasis only at word edges: implementation_plan.md and 2 * 3 * 4 stay as they are.
private val emphasis = Regex("""(?<![\w\\])(\*\*|__|~~|\*|_)(?=\S)(.+?)(?<=\S)\1(?!\w)""")
private val br = Regex("""<br\s*/?>""", RegexOption.IGNORE_CASE)
private val displayMath = Regex("""\$\$(.+?)\$\$""", RegexOption.DOT_MATCHES_ALL)
// $…$ only when it reads as math: no space inside the delimiters, no digit after ("$5 and $10").
private val inlineMath = Regex("""(?<![\\$\w])\$(?=\S)([^$\n]+?)(?<=\S)\$(?![\d$])""")
private val blankRuns = Regex("""\n{3,}""")

/**
 * Markdown as plain text that keeps its lines: links and code show their
 * text, headings, quotes and emphasis lose their marks, bullets become "•".
 * For notifications, previews and quotes, where Markdown isn't rendered.
 */
fun markdownToPlain(s: String): String {
    // Code spans first, so their contents (snake_case, a*b) are left alone.
    val codes = mutableListOf<String>()
    var t = s.replace("\r\n", "\n").replace(fence, "").replace(codeSpan) { m ->
        codes += m.groupValues[2].trim()
        "\u0000${codes.size - 1}\u0000"
    }
    t = t.replace(displayMath) { Tex.plain(it.groupValues[1].trim()) }.replace(inlineMath) { Tex.plain(it.groupValues[1]) }
    t = t.replace(tableRule, "").replace(rule, "")
        .replace(image, "$1").replace(link, "$1").replace(autolink, "$1")
        .replace(heading, "").replace(quote, "").replace(bullet, "$1• ").replace(br, "\n")
    repeat(2) { t = t.replace(emphasis, "$2") } // **_both_**
    t = t.replace(Regex("\u0000(\\d+)\u0000")) { m -> codes[m.groupValues[1].toInt()] }
    return t.replace(blankRuns, "\n\n").trim()
}
