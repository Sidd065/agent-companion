package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import io.github.sidd065.agentcompanion.ui.theme.codeStyle

data class DiffLine(val type: Char, val text: String, val oldNo: Int, val newNo: Int)

/** Line diff: trims common prefix/suffix, then Myers (bounded), falling back to replace. */
object LineDiff {
    fun diff(a: String?, b: String?): List<DiffLine> {
        val x = splitLines(a)
        val y = splitLines(b)
        var pre = 0
        while (pre < x.size && pre < y.size && x[pre] == y[pre]) pre++
        var suf = 0
        while (suf < x.size - pre && suf < y.size - pre && x[x.size - 1 - suf] == y[y.size - 1 - suf]) suf++
        val out = ArrayList<DiffLine>(x.size + y.size)
        for (i in 0 until pre) out.add(DiffLine(' ', x[i], i + 1, i + 1))
        val xm = x.subList(pre, x.size - suf)
        val ym = y.subList(pre, y.size - suf)
        val mid = myers(xm, ym) ?: run {
            xm.map { '-' to it } + ym.map { '+' to it }
        }
        var o = pre
        var n = pre
        for ((t, s) in mid) {
            when (t) {
                ' ' -> { o++; n++; out.add(DiffLine(' ', s, o, n)) }
                '-' -> { o++; out.add(DiffLine('-', s, o, 0)) }
                '+' -> { n++; out.add(DiffLine('+', s, 0, n)) }
            }
        }
        for (i in 0 until suf) {
            o++; n++
            out.add(DiffLine(' ', x[x.size - suf + i], o, n))
        }
        return out
    }

    fun splitLines(s: String?): List<String> {
        if (s.isNullOrEmpty()) return emptyList()
        val l = s.split('\n')
        return if (l.last().isEmpty()) l.dropLast(1) else l
    }

    /** Myers O(ND) with a cap on D; returns null when the cap is exceeded. */
    private fun myers(a: List<String>, b: List<String>, maxD: Int = 1500): List<Pair<Char, String>>? {
        val n = a.size
        val m = b.size
        if (n == 0) return b.map { '+' to it }
        if (m == 0) return a.map { '-' to it }
        val max = minOf(n + m, maxD)
        val offset = max + 1
        val v = IntArray(2 * max + 3)
        val trace = ArrayList<IntArray>()
        var found = false
        loop@ for (d in 0..max) {
            trace.add(v.copyOf())
            var k = -d
            while (k <= d) {
                var xx = if (k == -d || (k != d && v[k - 1 + offset] < v[k + 1 + offset])) v[k + 1 + offset] else v[k - 1 + offset] + 1
                var yy = xx - k
                while (xx < n && yy < m && a[xx] == b[yy]) { xx++; yy++ }
                v[k + offset] = xx
                if (xx >= n && yy >= m) { found = true; break@loop }
                k += 2
            }
        }
        if (!found) return null
        val res = ArrayList<Pair<Char, String>>()
        var xx = n
        var yy = m
        for (d in trace.size - 1 downTo 0) {
            val vv = trace[d]
            val k = xx - yy
            val prevK = if (k == -d || (k != d && vv[k - 1 + offset] < vv[k + 1 + offset])) k + 1 else k - 1
            val prevX = vv[prevK + offset]
            val prevY = prevX - prevK
            while (xx > prevX && yy > prevY) { res.add(' ' to a[xx - 1]); xx--; yy-- }
            if (d > 0) {
                if (xx == prevX) res.add('+' to b[yy - 1]) else res.add('-' to a[xx - 1])
            }
            xx = prevX
            yy = prevY
        }
        res.reverse()
        return res
    }

    fun stats(lines: List<DiffLine>): Pair<Int, Int> = lines.count { it.type == '+' } to lines.count { it.type == '-' }
}

/** One file of a unified (git) diff. */
data class UnifiedFile(val path: String, val header: List<String>, val lines: List<DiffLine>, val binary: Boolean) {
    val added get() = lines.count { it.type == '+' }
    val removed get() = lines.count { it.type == '-' }
}

object UnifiedDiff {
    private val hunkRe = Regex("""^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$""")

    fun parse(text: String): List<UnifiedFile> {
        val files = ArrayList<UnifiedFile>()
        var path = ""
        var header = ArrayList<String>()
        var lines = ArrayList<DiffLine>()
        var binary = false
        var o = 0
        var n = 0
        var inFile = false
        fun flush() {
            if (inFile) files.add(UnifiedFile(path, header, lines, binary))
        }
        for (raw in text.split('\n')) {
            if (raw.startsWith("diff --git ")) {
                flush()
                inFile = true
                header = arrayListOf(raw)
                lines = ArrayList()
                binary = false
                val parts = raw.removePrefix("diff --git ").split(" b/")
                path = parts.lastOrNull() ?: raw
                continue
            }
            if (!inFile) continue
            val hm = hunkRe.find(raw)
            when {
                raw.startsWith("+++ ") -> {
                    header.add(raw)
                    val p = raw.removePrefix("+++ ").removePrefix("b/")
                    if (p != "/dev/null") path = p
                }
                raw.startsWith("--- ") && lines.isEmpty() -> header.add(raw)
                raw.startsWith("Binary files") -> { binary = true; header.add(raw) }
                hm != null -> {
                    o = hm.groupValues[1].toInt()
                    n = hm.groupValues[2].toInt()
                    lines.add(DiffLine('@', raw, 0, 0))
                }
                raw.startsWith("+") -> { lines.add(DiffLine('+', raw.substring(1), 0, n)); n++ }
                raw.startsWith("-") -> { lines.add(DiffLine('-', raw.substring(1), o, 0)); o++ }
                raw.startsWith(" ") -> { lines.add(DiffLine(' ', raw.substring(1), o, n)); o++; n++ }
                raw.startsWith("\\") -> {}
                lines.isEmpty() -> header.add(raw)
            }
        }
        flush()
        return files
    }
}

/** Renders diff lines with context folding. */
@Composable
fun DiffView(
    lines: List<DiffLine>,
    modifier: Modifier = Modifier,
    context: Int = 3,
    wrap: Boolean = false,
    maxLines: Int = Int.MAX_VALUE,
    onLineClick: ((DiffLine) -> Unit)? = null,
    marked: (DiffLine) -> Boolean = { false },
) {
    val ex = LocalExtra.current
    // Collapse long unchanged runs.
    val shown = remember(lines, context) {
        val keep = BooleanArray(lines.size)
        lines.forEachIndexed { i, l ->
            if (l.type != ' ') for (j in maxOf(0, i - context)..minOf(lines.size - 1, i + context)) keep[j] = true
        }
        if (lines.none { it.type != ' ' }) for (i in lines.indices) keep[i] = i < 20
        val out = ArrayList<Any>()
        var skipped = 0
        lines.forEachIndexed { i, l ->
            if (keep[i]) {
                if (skipped > 0) { out.add(skipped); skipped = 0 }
                out.add(l)
            } else skipped++
        }
        if (skipped > 0) out.add(skipped)
        out
    }
    var limit by remember { mutableStateOf(maxLines) }
    val hs = rememberScrollState()
    BoxWithConstraints(modifier.background(ex.codeBg)) {
    val minW = maxWidth
    // Every row spans the widest line (and at least the viewport) so highlights are even.
    Column((if (wrap) Modifier.fillMaxWidth() else Modifier.horizontalScroll(hs).width(IntrinsicSize.Max).widthIn(min = minW))) {
        shown.take(limit).forEach { e ->
            when (e) {
                is Int -> Text(
                    "  ⋯ $e unchanged line${if (e == 1) "" else "s"}",
                    style = codeStyle, color = ex.muted,
                    modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
                )
                is DiffLine -> DiffRow(e, wrap, onLineClick, marked(e))
            }
        }
        if (shown.size > limit) {
            Text(
                "Show ${shown.size - limit} more lines",
                style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.clickable { limit = Int.MAX_VALUE }.padding(10.dp),
            )
        }
    }
    }
}

@Composable
private fun DiffRow(l: DiffLine, wrap: Boolean, onClick: ((DiffLine) -> Unit)? = null, marked: Boolean = false) {
    val ex = LocalExtra.current
    val (bg, fg) = when (l.type) {
        '+' -> ex.diffAddBg to ex.diffAdd
        '-' -> ex.diffDelBg to ex.diffDel
        '@' -> MaterialTheme.colorScheme.secondaryContainer.copy(alpha = 0.35f) to MaterialTheme.colorScheme.onSecondaryContainer
        else -> Color.Transparent to MaterialTheme.colorScheme.onSurface
    }
    Row(
        Modifier.fillMaxWidth().background(if (marked) ex.warning.copy(alpha = 0.25f) else bg)
            .then(if (onClick != null && l.type != '@') Modifier.clickable { onClick(l) } else Modifier),
    ) {
        if (l.type != '@') {
            Text(
                (if (l.type == '+') "" else l.oldNo.takeIf { it > 0 }?.toString() ?: "").padStart(4),
                style = codeStyle, color = ex.muted, modifier = Modifier.width(36.dp).padding(start = 2.dp),
            )
            Text(
                (if (l.type == '-') "" else l.newNo.takeIf { it > 0 }?.toString() ?: "").padStart(4),
                style = codeStyle, color = ex.muted, modifier = Modifier.width(36.dp),
            )
            Text(
                (if (l.type == ' ') " " else l.type.toString()) + " " + l.text.replace("\t", "    "),
                style = codeStyle, color = fg, softWrap = wrap, overflow = TextOverflow.Clip,
                modifier = Modifier.padding(end = 12.dp),
            )
        } else {
            Text(l.text, style = codeStyle, color = fg, softWrap = wrap, modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp))
        }
    }
}
