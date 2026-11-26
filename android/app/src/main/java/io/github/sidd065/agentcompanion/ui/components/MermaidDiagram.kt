package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.rememberTransformableState
import androidx.compose.foundation.gestures.transformable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Fullscreen
import androidx.compose.material.icons.outlined.Schema
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import io.github.sidd065.agentcompanion.ui.theme.codeStyle
import kotlin.math.atan2
import kotlin.math.cos
import kotlin.math.max
import kotlin.math.min
import kotlin.math.sin

// ==================== AST & Pure Parser (Unit-Testable) ====================

enum class DiagramDirection { TD, LR, BT, RL }
enum class NodeShape { RECT, ROUND, STADIUM, SUBROUTINE, DIAMOND, CIRCLE, START_END }
enum class EdgeKind { SOLID, DOTTED, THICK }

data class MermaidNode(
    val id: String,
    val label: String,
    val shape: NodeShape = NodeShape.ROUND,
    val details: List<String> = emptyList(),
    val subgraph: String? = null,
)

data class MermaidEdge(
    val from: String,
    val to: String,
    val label: String = "",
    val kind: EdgeKind = EdgeKind.SOLID,
    val arrow: Boolean = true,
)

data class MermaidSubgraph(val id: String, val title: String, val nodeIds: List<String>)

data class SequenceActor(val id: String, val label: String)
data class SequenceEvent(
    val from: String,
    val to: String,
    val text: String,
    val dashed: Boolean = false,
    val isNote: Boolean = false,
)

data class XySeries(val kind: String, val values: List<Float>)

sealed interface MermaidAst {
    val title: String
    data class Graph(
        override val title: String = "",
        val direction: DiagramDirection = DiagramDirection.TD,
        val nodes: List<MermaidNode>,
        val edges: List<MermaidEdge>,
        val subgraphs: List<MermaidSubgraph> = emptyList(),
    ) : MermaidAst

    data class Sequence(
        override val title: String = "",
        val actors: List<SequenceActor>,
        val events: List<SequenceEvent>,
    ) : MermaidAst

    data class XyChart(
        override val title: String = "",
        val xLabels: List<String>,
        val yTitle: String = "",
        val yMin: Float? = null,
        val yMax: Float? = null,
        val series: List<XySeries>,
    ) : MermaidAst
}

private val edgeOpRegex = Regex(
    """(==>\s*\|[^|]*\||-\.->\s*\|[^|]*\||-->\s*\|[^|]*\||--\s+[^-=>]+\s+-->|-\.\s+[^.]+\s+\.->|==\s+[^=]+\s+==>|==>|<-->|-\.->|-->|---|-.-|===|<\|--|\*--|o--|\|\|--o\{|\}o--\|\||\|\|--\|\||\.\.>|--\>)"""
)

fun parseMermaidDiagram(raw: String): MermaidAst? {
    val lines = raw.replace("\r\n", "\n")
        .lines()
        .map { it.trim() }
        .filter { it.isNotEmpty() && !it.startsWith("%%") }
    if (lines.isEmpty()) return null

    // Strip optional YAML frontmatter
    var idx = 0
    var docTitle = ""
    if (lines[0] == "---") {
        idx = 1
        while (idx < lines.size && lines[idx] != "---") {
            if (lines[idx].startsWith("title:", ignoreCase = true)) {
                docTitle = lines[idx].substringAfter(':').trim().trim('"', '\'')
            }
            idx++
        }
        if (idx < lines.size && lines[idx] == "---") idx++
    }
    if (idx >= lines.size) return null

    val header = lines[idx]
    val body = lines.subList(idx + 1, lines.size)
    val headLower = header.lowercase()
    return when {
        headLower.startsWith("sequencediagram") -> parseSequence(docTitle, body)
        headLower.startsWith("xychart") -> parseXyChart(docTitle, body)
        headLower.startsWith("statediagram") -> parseStateDiagram(docTitle, body)
        headLower.startsWith("classdiagram") -> parseClassOrErDiagram(docTitle, body, isEr = false)
        headLower.startsWith("erdiagram") -> parseClassOrErDiagram(docTitle, body, isEr = true)
        headLower.startsWith("flowchart") || headLower.startsWith("graph") -> {
            val dirTok = header.split(Regex("\\s+")).getOrNull(1)?.uppercase().orEmpty()
            val dir = when (dirTok) {
                "LR" -> DiagramDirection.LR
                "RL" -> DiagramDirection.RL
                "BT" -> DiagramDirection.BT
                else -> DiagramDirection.TD
            }
            parseFlowchart(docTitle, dir, body)
        }
        else -> null
    }
}

private fun unquoteMermaidLabel(s: String): String =
    s.trim()
        .removeSurrounding("\"")
        .removeSurrounding("'")
        .replace("<br/>", "\n", ignoreCase = true)
        .replace("<br>", "\n", ignoreCase = true)
        .replace("\\n", "\n")
        .trim()

private fun parseNodeSpec(raw: String, currentSubgraph: String?): MermaidNode? {
    var t = raw.trim().removeSuffix(";")
    // Strip Mermaid style class shorthand `:::className`
    val classIdx = t.indexOf(":::")
    if (classIdx > 0) t = t.substring(0, classIdx).trim()
    if (t.isEmpty()) return null
    if (t == "[*]") {
        return MermaidNode(id = "__star__", label = "●", shape = NodeShape.START_END, subgraph = currentSubgraph)
    }

    // Find first bracket/paren/brace outside quotes
    var inQuote = false
    var openPos = -1
    for (i in t.indices) {
        val ch = t[i]
        if (ch == '"') inQuote = !inQuote
        if (!inQuote && (ch == '[' || ch == '(' || ch == '{')) {
            openPos = i
            break
        }
    }
    if (openPos <= 0) {
        val cleanId = unquoteMermaidLabel(t)
        if (cleanId.isEmpty()) return null
        return MermaidNode(id = cleanId, label = cleanId, shape = NodeShape.ROUND, subgraph = currentSubgraph)
    }

    val id = t.substring(0, openPos).trim()
    val rest = t.substring(openPos).trim()
    val (shape, inner) = when {
        rest.startsWith("([") && rest.endsWith("])") -> NodeShape.STADIUM to rest.substring(2, rest.length - 2)
        rest.startsWith("[[") && rest.endsWith("]]") -> NodeShape.SUBROUTINE to rest.substring(2, rest.length - 2)
        rest.startsWith("((") && rest.endsWith("))") -> NodeShape.CIRCLE to rest.substring(2, rest.length - 2)
        rest.startsWith("{{") && rest.endsWith("}}") -> NodeShape.DIAMOND to rest.substring(2, rest.length - 2)
        rest.startsWith("[") && rest.endsWith("]") -> NodeShape.RECT to rest.substring(1, rest.length - 1)
        rest.startsWith("(") && rest.endsWith(")") -> NodeShape.ROUND to rest.substring(1, rest.length - 1)
        rest.startsWith("{") && rest.endsWith("}") -> NodeShape.DIAMOND to rest.substring(1, rest.length - 1)
        else -> NodeShape.ROUND to rest
    }
    val label = unquoteMermaidLabel(inner).ifEmpty { id }
    return MermaidNode(id = id, label = label, shape = shape, subgraph = currentSubgraph)
}

/** Splits a flowchart statement into alternating node-group strings and edge tokens, respecting quotes and brackets. */
private data class EdgeSep(val start: Int, val end: Int, val kind: EdgeKind, val arrow: Boolean, val label: String)

private fun findEdgeSeparators(line: String): List<EdgeSep> {
    val masked = CharArray(line.length) { i -> line[i] }
    var inQuote = false
    var depth = 0
    for (i in line.indices) {
        val c = line[i]
        if (c == '"') {
            inQuote = !inQuote
            masked[i] = ' '
            continue
        }
        if (inQuote) {
            masked[i] = ' '
            continue
        }
        when (c) {
            '[', '(', '{' -> {
                depth++
                masked[i] = ' '
            }
            ']', ')', '}' -> {
                masked[i] = ' '
                depth = (depth - 1).coerceAtLeast(0)
            }
            else -> if (depth > 0) masked[i] = ' '
        }
    }
    val maskedStr = String(masked)
    val out = ArrayList<EdgeSep>()
    for (m in edgeOpRegex.findAll(maskedStr)) {
        val rawOp = line.substring(m.range.first, m.range.last + 1).trim()
        var label = ""
        if (rawOp.contains('|')) {
            label = unquoteMermaidLabel(rawOp.substringAfter('|').substringBeforeLast('|'))
        } else if (rawOp.startsWith("-- ") && rawOp.endsWith("-->")) {
            label = unquoteMermaidLabel(rawOp.removePrefix("--").removeSuffix("-->"))
        } else if (rawOp.startsWith("-. ") && rawOp.endsWith(".->")) {
            label = unquoteMermaidLabel(rawOp.removePrefix("-.").removeSuffix(".->"))
        } else if (rawOp.startsWith("== ") && rawOp.endsWith("==>")) {
            label = unquoteMermaidLabel(rawOp.removePrefix("==").removeSuffix("==>"))
        }
        val kind = when {
            rawOp.contains("-.") || rawOp.contains("..") -> EdgeKind.DOTTED
            rawOp.startsWith("==") -> EdgeKind.THICK
            else -> EdgeKind.SOLID
        }
        val arrow = rawOp.contains('>') || rawOp.contains('<') || rawOp.contains('{')
        out += EdgeSep(m.range.first, m.range.last + 1, kind, arrow, label)
    }
    return out
}

private fun splitAmpersandOutsideQuotes(segment: String): List<String> {
    val parts = ArrayList<String>()
    var start = 0
    var inQuote = false
    var depth = 0
    for (i in segment.indices) {
        val c = segment[i]
        if (c == '"') inQuote = !inQuote
        else if (!inQuote) {
            when (c) {
                '[', '(', '{' -> depth++
                ']', ')', '}' -> depth = (depth - 1).coerceAtLeast(0)
                '&' -> if (depth == 0) {
                    val sub = segment.substring(start, i).trim()
                    if (sub.isNotEmpty()) parts += sub
                    start = i + 1
                }
            }
        }
    }
    val tail = segment.substring(start).trim()
    if (tail.isNotEmpty()) parts += tail
    return parts
}

private fun parseFlowchart(initialTitle: String, initialDir: DiagramDirection, body: List<String>): MermaidAst.Graph? {
    var title = initialTitle
    var dir = initialDir
    val nodes = LinkedHashMap<String, MermaidNode>()
    val edges = ArrayList<MermaidEdge>()
    val subgraphs = ArrayList<MermaidSubgraph>()
    val subStack = ArrayDeque<Pair<String, Pair<String, MutableList<String>>>>()
    var starCounter = 0

    fun upsertNode(spec: String): String? {
        val curSub = subStack.lastOrNull()?.first
        val parsed = parseNodeSpec(spec, curSub) ?: return null
        val effectiveId = if (parsed.id == "__star__") "__star_${++starCounter}" else parsed.id
        val actual = if (effectiveId != parsed.id) parsed.copy(id = effectiveId) else parsed
        val prev = nodes[effectiveId]
        if (prev == null) {
            nodes[effectiveId] = actual
        } else {
            val betterLabel = if (actual.label != effectiveId || prev.label == effectiveId) actual.label else prev.label
            val betterShape = if (actual.label != effectiveId) actual.shape else prev.shape
            nodes[effectiveId] = prev.copy(
                label = betterLabel,
                shape = betterShape,
                subgraph = prev.subgraph ?: curSub,
            )
        }
        subStack.lastOrNull()?.second?.second?.let { list ->
            if (effectiveId !in list) list += effectiveId
        }
        return effectiveId
    }

    for (rawLine in body) {
        val line = rawLine.trim().removeSuffix(";")
        if (line.isEmpty() || line.startsWith("%%")) continue
        val lower = line.lowercase()
        when {
            lower.startsWith("direction ") -> {
                when (line.substringAfter(' ').trim().uppercase()) {
                    "LR" -> if (subStack.isEmpty()) dir = DiagramDirection.LR
                    "RL" -> if (subStack.isEmpty()) dir = DiagramDirection.RL
                    "BT" -> if (subStack.isEmpty()) dir = DiagramDirection.BT
                    "TD", "TB" -> if (subStack.isEmpty()) dir = DiagramDirection.TD
                }
                continue
            }
            lower.startsWith("accTitle:") || lower.startsWith("title ") -> {
                title = unquoteMermaidLabel(line.substringAfter(' ').substringAfter(':'))
                continue
            }
            lower.startsWith("classdef ") || lower.startsWith("class ") ||
                lower.startsWith("style ") || lower.startsWith("linkstyle ") || lower.startsWith("click ") -> continue
            lower.startsWith("subgraph ") -> {
                val rest = line.substring("subgraph ".length).trim()
                val parsed = parseNodeSpec(rest, null)
                val sgId = parsed?.id ?: "sg_${subgraphs.size + 1}"
                val sgTitle = parsed?.label ?: unquoteMermaidLabel(rest)
                subStack.addLast(sgId to (sgTitle to mutableListOf()))
                continue
            }
            lower == "end" -> {
                val popped = subStack.removeLastOrNull()
                if (popped != null && popped.second.second.isNotEmpty()) {
                    subgraphs += MermaidSubgraph(popped.first, popped.second.first, popped.second.second)
                }
                continue
            }
        }

        val seps = findEdgeSeparators(line)
        if (seps.isEmpty()) {
            splitAmpersandOutsideQuotes(line).forEach { upsertNode(it) }
        } else {
            val groups = ArrayList<List<String>>()
            var pos = 0
            for (s in seps) {
                val seg = line.substring(pos, s.start).trim()
                groups += splitAmpersandOutsideQuotes(seg).mapNotNull { upsertNode(it) }
                pos = s.end
            }
            val lastSeg = line.substring(pos).trim()
            groups += splitAmpersandOutsideQuotes(lastSeg).mapNotNull { upsertNode(it) }

            for (i in seps.indices) {
                val sep = seps[i]
                val left = groups.getOrNull(i).orEmpty()
                val right = groups.getOrNull(i + 1).orEmpty()
                for (u in left) {
                    for (v in right) {
                        edges += MermaidEdge(from = u, to = v, label = sep.label, kind = sep.kind, arrow = sep.arrow)
                    }
                }
            }
        }
    }
    while (subStack.isNotEmpty()) {
        val popped = subStack.removeLast()
        if (popped.second.second.isNotEmpty()) {
            subgraphs += MermaidSubgraph(popped.first, popped.second.first, popped.second.second)
        }
    }
    if (nodes.isEmpty()) return null
    return MermaidAst.Graph(
        title = title,
        direction = dir,
        nodes = nodes.values.toList(),
        edges = edges,
        subgraphs = subgraphs,
    )
}

private fun parseStateDiagram(initialTitle: String, body: List<String>): MermaidAst.Graph? {
    val converted = ArrayList<String>()
    for (raw in body) {
        val l = raw.trim()
        if (l.isEmpty() || l.startsWith("%%")) continue
        val alias = Regex("""^state\s+"([^"]+)"\s+as\s+(\S+)$""", RegexOption.IGNORE_CASE).find(l)
        if (alias != null) {
            converted += "${alias.groupValues[2]}[\"${alias.groupValues[1]}\"]"
            continue
        }
        val colonIdx = l.indexOf(':')
        if (colonIdx > 0 && l.substring(0, colonIdx).contains("-->")) {
            val left = l.substring(0, colonIdx).trim()
            val lbl = l.substring(colonIdx + 1).trim()
            converted += if (lbl.isNotEmpty()) left.replace("-->", "-->|$lbl|") else left
        } else if (!l.equals("state {", true) && l != "{") {
            converted += l
        }
    }
    return parseFlowchart(initialTitle, DiagramDirection.TD, converted)
}

private fun parseClassOrErDiagram(initialTitle: String, body: List<String>, isEr: Boolean): MermaidAst.Graph? {
    val nodes = LinkedHashMap<String, MermaidNode>()
    val edges = ArrayList<MermaidEdge>()
    var currentBlockId: String? = null
    val currentMembers = ArrayList<String>()

    fun flushBlock() {
        val id = currentBlockId ?: return
        val prev = nodes[id]
        nodes[id] = MermaidNode(
            id = id,
            label = prev?.label ?: id,
            shape = NodeShape.RECT,
            details = (prev?.details.orEmpty() + currentMembers).take(8),
        )
        currentBlockId = null
        currentMembers.clear()
    }

    val relRe = Regex("""^(\S+)\s+([<|*>o.{}-]{2,})\s+(\S+)(?:\s*:\s*(.+))?$""")
    for (raw in body) {
        val l = raw.trim().removeSuffix(";")
        if (l.isEmpty() || l.startsWith("%%")) continue
        if (currentBlockId != null) {
            if (l == "}") {
                flushBlock()
            } else {
                currentMembers += l
            }
            continue
        }
        val classStart = Regex("""^(?:class\s+)?([A-Za-z0-9_-]+)\s*\{$""").find(l)
        if (classStart != null) {
            currentBlockId = classStart.groupValues[1]
            continue
        }
        val classDecl = Regex("""^class\s+([A-Za-z0-9_-]+)$""").find(l)
        if (classDecl != null) {
            val id = classDecl.groupValues[1]
            nodes.putIfAbsent(id, MermaidNode(id = id, label = id, shape = NodeShape.RECT))
            continue
        }
        val rel = relRe.find(l)
        if (rel != null) {
            val a = unquoteMermaidLabel(rel.groupValues[1])
            val op = rel.groupValues[2]
            val b = unquoteMermaidLabel(rel.groupValues[3])
            val lbl = unquoteMermaidLabel(rel.groupValues[4])
            nodes.putIfAbsent(a, MermaidNode(id = a, label = a, shape = NodeShape.RECT))
            nodes.putIfAbsent(b, MermaidNode(id = b, label = b, shape = NodeShape.RECT))
            edges += MermaidEdge(
                from = a,
                to = b,
                label = if (isEr && lbl.isEmpty()) op else lbl,
                kind = if (op.contains("..")) EdgeKind.DOTTED else EdgeKind.SOLID,
                arrow = true,
            )
        }
    }
    flushBlock()
    if (nodes.isEmpty()) return null
    return MermaidAst.Graph(title = initialTitle, direction = DiagramDirection.TD, nodes = nodes.values.toList(), edges = edges)
}

private fun parseSequence(initialTitle: String, body: List<String>): MermaidAst.Sequence? {
    var title = initialTitle
    val actors = LinkedHashMap<String, SequenceActor>()
    val events = ArrayList<SequenceEvent>()

    fun ensureActor(id: String) {
        val clean = id.trim()
        if (clean.isNotEmpty() && clean !in actors) {
            actors[clean] = SequenceActor(clean, clean)
        }
    }

    val partRe = Regex("""^(?:participant|actor)\s+([^\s]+)(?:\s+as\s+(.+))?$""", RegexOption.IGNORE_CASE)
    val msgRe = Regex("""^([^-:<>\s]+)\s*(--?>?>|--?[xX)]|--?>>?)\s*([^-:<>\s]+)\s*:\s*(.*)$""")
    val noteRe = Regex("""^note\s+(?:over|left of|right of)\s+([^:]+):\s*(.+)$""", RegexOption.IGNORE_CASE)

    for (raw in body) {
        val l = raw.trim()
        if (l.isEmpty() || l.startsWith("%%")) continue
        if (l.startsWith("title ", true) || l.startsWith("title:", true)) {
            title = unquoteMermaidLabel(l.substringAfter(' ').substringAfter(':'))
            continue
        }
        partRe.find(l)?.let { m ->
            val id = unquoteMermaidLabel(m.groupValues[1])
            val lbl = unquoteMermaidLabel(m.groupValues[2]).ifEmpty { id }
            actors[id] = SequenceActor(id, lbl)
            continue
        }
        noteRe.find(l)?.let { m ->
            val targets = m.groupValues[1].split(',').map { it.trim() }.filter { it.isNotEmpty() }
            val a = targets.firstOrNull() ?: continue
            val b = targets.lastOrNull() ?: a
            ensureActor(a)
            ensureActor(b)
            events += SequenceEvent(from = a, to = b, text = unquoteMermaidLabel(m.groupValues[2]), isNote = true)
            continue
        }
        msgRe.find(l)?.let { m ->
            val from = unquoteMermaidLabel(m.groupValues[1])
            val arrow = m.groupValues[2]
            val to = unquoteMermaidLabel(m.groupValues[3])
            val txt = unquoteMermaidLabel(m.groupValues[4])
            ensureActor(from)
            ensureActor(to)
            events += SequenceEvent(from = from, to = to, text = txt, dashed = arrow.startsWith("--"))
        }
    }
    if (actors.isEmpty()) return null
    return MermaidAst.Sequence(title = title, actors = actors.values.toList(), events = events)
}

private fun parseBracketList(raw: String): List<String> {
    val inside = raw.substringAfter('[', "").substringBeforeLast(']', "")
    if (inside.isBlank()) return emptyList()
    return inside.split(',').map { unquoteMermaidLabel(it) }.filter { it.isNotEmpty() }
}

private fun parseXyChart(initialTitle: String, body: List<String>): MermaidAst.XyChart? {
    var title = initialTitle
    var xLabels = emptyList<String>()
    var yTitle = ""
    var yMin: Float? = null
    var yMax: Float? = null
    val series = ArrayList<XySeries>()

    for (raw in body) {
        val l = raw.trim()
        val lower = l.lowercase()
        when {
            lower.startsWith("title ") -> title = unquoteMermaidLabel(l.substring(6))
            lower.startsWith("x-axis") -> xLabels = parseBracketList(l)
            lower.startsWith("y-axis") -> {
                val q = Regex(""""([^"]+)"""").find(l)?.groupValues?.get(1)
                if (q != null) yTitle = q
                val range = Regex("""(-?\d+(?:\.\d+)?)\s*-->\s*(-?\d+(?:\.\d+)?)""").find(l)
                if (range != null) {
                    yMin = range.groupValues[1].toFloatOrNull()
                    yMax = range.groupValues[2].toFloatOrNull()
                }
            }
            lower.startsWith("bar") -> {
                val vals = parseBracketList(l).mapNotNull { it.toFloatOrNull() }
                if (vals.isNotEmpty()) series += XySeries("bar", vals)
            }
            lower.startsWith("line") -> {
                val vals = parseBracketList(l).mapNotNull { it.toFloatOrNull() }
                if (vals.isNotEmpty()) series += XySeries("line", vals)
            }
        }
    }
    if (series.isEmpty()) return null
    return MermaidAst.XyChart(title = title, xLabels = xLabels, yTitle = yTitle, yMin = yMin, yMax = yMax, series = series)
}

// ==================== Layout Engine (Pure Geometry) ====================

data class PositionedNode(val node: MermaidNode, val x: Float, val y: Float, val w: Float, val h: Float)

data class GraphLayout(
    val width: Float,
    val height: Float,
    val nodes: Map<String, PositionedNode>,
)

fun computeGraphLayout(
    graph: MermaidAst.Graph,
    nodeSize: (MermaidNode) -> Pair<Float, Float>,
    rankGap: Float = 68f,
    nodeGap: Float = 26f,
    pad: Float = 24f,
): GraphLayout {
    val nodeMap = graph.nodes.associateBy { it.id }
    val adj = HashMap<String, MutableList<String>>()
    val indeg = HashMap<String, Int>()
    for (n in graph.nodes) {
        adj[n.id] = mutableListOf()
        indeg[n.id] = 0
    }
    for (e in graph.edges) {
        if (e.from != e.to && e.from in nodeMap && e.to in nodeMap) {
            adj[e.from]!!.add(e.to)
            indeg[e.to] = (indeg[e.to] ?: 0) + 1
        }
    }

    // Cycle-safe rank assignment via Kahn + longest-path relaxation
    val rank = HashMap<String, Int>()
    val remaining = HashSet(graph.nodes.map { it.id })
    val curIndeg = HashMap(indeg)
    while (remaining.isNotEmpty()) {
        val ready = graph.nodes.map { it.id }.filter { it in remaining && (curIndeg[it] ?: 0) <= 0 }
        val picks = ready.ifEmpty { listOf(graph.nodes.first { it.id in remaining }.id) }
        for (u in picks) {
            remaining.remove(u)
            val rU = rank[u] ?: 0
            rank[u] = rU
            for (v in adj[u].orEmpty()) {
                if (v in remaining) {
                    rank[v] = max(rank[v] ?: 0, rU + 1)
                    curIndeg[v] = (curIndeg[v] ?: 1) - 1
                }
            }
        }
    }

    val maxRank = (rank.values.maxOrNull() ?: 0).coerceAtLeast(0)
    val layers = Array(maxRank + 1) { ArrayList<MermaidNode>() }
    for (n in graph.nodes) {
        layers[rank[n.id] ?: 0].add(n)
    }

    // One pass of barycenter ordering to reduce edge crossings
    val pred = HashMap<String, MutableList<String>>()
    for (e in graph.edges) {
        if (e.from in nodeMap && e.to in nodeMap) pred.getOrPut(e.to) { mutableListOf() }.add(e.from)
    }
    for (r in 1..maxRank) {
        val prevOrder = layers[r - 1].mapIndexed { idx, n -> n.id to idx }.toMap()
        layers[r].sortBy { n ->
            val ps = pred[n.id].orEmpty().mapNotNull { prevOrder[it] }
            if (ps.isEmpty()) Int.MAX_VALUE.toFloat() else ps.average().toFloat()
        }
    }

    val sizes = graph.nodes.associate { it.id to nodeSize(it) }
    val horizontal = graph.direction == DiagramDirection.LR || graph.direction == DiagramDirection.RL
    val orderedLayers = if (graph.direction == DiagramDirection.RL || graph.direction == DiagramDirection.BT) {
        layers.reversedArray()
    } else {
        layers
    }

    val positioned = LinkedHashMap<String, PositionedNode>()
    if (horizontal) {
        val colWidths = orderedLayers.map { col -> col.maxOfOrNull { sizes[it.id]!!.first } ?: 120f }
        val colHeights = orderedLayers.map { col ->
            col.sumOf { sizes[it.id]!!.second.toDouble() }.toFloat() + max(0, col.size - 1) * nodeGap
        }
        val totalH = max(120f, (colHeights.maxOrNull() ?: 120f) + pad * 2)
        var curX = pad
        for (c in orderedLayers.indices) {
            val col = orderedLayers[c]
            val colW = colWidths[c]
            var curY = (totalH - colHeights[c]) / 2f
            for (n in col) {
                val (w, h) = sizes[n.id]!!
                positioned[n.id] = PositionedNode(n, curX + (colW - w) / 2f, curY, w, h)
                curY += h + nodeGap
            }
            curX += colW + rankGap
        }
        val totalW = max(200f, curX - rankGap + pad)
        return GraphLayout(totalW, totalH, positioned)
    } else {
        val rowHeights = orderedLayers.map { row -> row.maxOfOrNull { sizes[it.id]!!.second } ?: 52f }
        val rowWidths = orderedLayers.map { row ->
            row.sumOf { sizes[it.id]!!.first.toDouble() }.toFloat() + max(0, row.size - 1) * nodeGap
        }
        val totalW = max(220f, (rowWidths.maxOrNull() ?: 220f) + pad * 2)
        var curY = pad
        for (r in orderedLayers.indices) {
            val row = orderedLayers[r]
            val rowH = rowHeights[r]
            var curX = (totalW - rowWidths[r]) / 2f
            for (n in row) {
                val (w, h) = sizes[n.id]!!
                positioned[n.id] = PositionedNode(n, curX, curY + (rowH - h) / 2f, w, h)
                curX += w + nodeGap
            }
            curY += rowH + rankGap
        }
        val totalH = max(120f, curY - rankGap + pad)
        return GraphLayout(totalW, totalH, positioned)
    }
}

// ==================== Compose Vector Renderer ====================

@Composable
fun MermaidDiagram(source: String, modifier: Modifier = Modifier) {
    val ctx = LocalContext.current
    val ex = LocalExtra.current
    val ast = remember(source) { runCatching { parseMermaidDiagram(source) }.getOrNull() }
    var showCode by remember(source) { mutableStateOf(ast == null) }
    var fullscreen by remember { mutableStateOf(false) }

    Surface(
        modifier = modifier.fillMaxWidth().padding(vertical = 4.dp),
        shape = RoundedCornerShape(12.dp),
        color = ex.codeBg,
        border = androidx.compose.foundation.BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f)),
    ) {
        Column(Modifier.fillMaxWidth()) {
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.Outlined.Schema, null, Modifier.size(15.dp), tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(6.dp))
                Text(
                    ast?.title?.takeIf { it.isNotBlank() } ?: "MERMAID",
                    style = MaterialTheme.typography.labelSmall,
                    color = ex.muted,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                    modifier = Modifier.weight(1f),
                )
                if (ast != null) {
                    Row(
                        Modifier
                            .clip(RoundedCornerShape(50))
                            .background(MaterialTheme.colorScheme.surfaceContainerHighest)
                            .clickable { showCode = !showCode }
                            .padding(horizontal = 8.dp, vertical = 3.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(if (showCode) Icons.Outlined.Schema else Icons.Outlined.Code, null, Modifier.size(13.dp))
                        Spacer(Modifier.width(4.dp))
                        Text(if (showCode) "Diagram" else "Code", style = MaterialTheme.typography.labelSmall)
                    }
                    IconButton(onClick = { fullscreen = true }, modifier = Modifier.size(30.dp)) {
                        Icon(Icons.Outlined.Fullscreen, "Expand diagram", Modifier.size(17.dp), tint = ex.muted)
                    }
                }
                IconButton(onClick = { ctx.copyText(source.trim(), "Mermaid diagram") }, modifier = Modifier.size(30.dp)) {
                    Icon(Icons.Outlined.ContentCopy, "Copy Mermaid source", Modifier.size(15.dp), tint = ex.muted)
                }
            }
            if (showCode || ast == null) {
                val hs = rememberScrollState()
                SelectionContainer {
                    Text(
                        source.trim(),
                        style = codeStyle,
                        color = MaterialTheme.colorScheme.onSurface,
                        modifier = Modifier.fillMaxWidth().horizontalScroll(hs).padding(horizontal = 12.dp, vertical = 8.dp),
                    )
                }
            } else {
                val hs = rememberScrollState()
                Column(Modifier.fillMaxWidth()) {
                    Box(Modifier.fillMaxWidth().horizontalScroll(hs).padding(bottom = 6.dp)) {
                        MermaidCanvas(ast)
                    }
                    if (hs.maxValue > 24) {
                        Text(
                            "Swipe diagram horizontally or tap ⛶ to expand →",
                            style = MaterialTheme.typography.labelSmall,
                            color = LocalExtra.current.muted,
                            modifier = Modifier.padding(horizontal = 12.dp, vertical = 4.dp),
                        )
                    }
                }
            }
        }
    }

    if (fullscreen && ast != null) {
        Dialog(
            onDismissRequest = { fullscreen = false },
            properties = DialogProperties(usePlatformDefaultWidth = false),
        ) {
            var scale by remember { mutableFloatStateOf(1f) }
            var offset by remember { mutableStateOf(Offset.Zero) }
            val tf = rememberTransformableState { zoom, pan, _ ->
                scale = (scale * zoom).coerceIn(0.5f, 4f)
                offset = if (scale == 1f) Offset.Zero else offset + pan
            }
            Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                Box(Modifier.fillMaxSize()) {
                    Box(
                        Modifier
                            .fillMaxSize()
                            .transformable(tf)
                            .horizontalScroll(rememberScrollState())
                            .verticalScroll(rememberScrollState())
                            .padding(24.dp),
                        contentAlignment = Alignment.Center,
                    ) {
                        Box(Modifier.graphicsLayer(scaleX = scale, scaleY = scale, translationX = offset.x, translationY = offset.y)) {
                            MermaidCanvas(ast)
                        }
                    }
                    IconButton(
                        onClick = { fullscreen = false },
                        modifier = Modifier.align(Alignment.TopEnd).statusBarsPadding().padding(16.dp),
                    ) {
                        Icon(Icons.Outlined.Close, "Close")
                    }
                }
            }
        }
    }
}

@Composable
private fun MermaidCanvas(ast: MermaidAst) {
    when (ast) {
        is MermaidAst.Graph -> GraphDiagramCanvas(ast)
        is MermaidAst.Sequence -> SequenceDiagramCanvas(ast)
        is MermaidAst.XyChart -> XyChartCanvas(ast)
    }
}

@Composable
private fun GraphDiagramCanvas(graph: MermaidAst.Graph) {
    val measurer = rememberTextMeasurer()
    val density = LocalDensity.current
    val cs = MaterialTheme.colorScheme
    val ex = LocalExtra.current
    val labelStyle = MaterialTheme.typography.bodySmall.copy(fontSize = 12.sp, lineHeight = 16.sp, fontWeight = FontWeight.Medium)
    val detailStyle = MaterialTheme.typography.labelSmall.copy(fontFamily = FontFamily.Monospace, fontSize = 10.sp)
    val edgeLabelStyle = MaterialTheme.typography.labelSmall.copy(fontSize = 10.sp)

    val maxLabelPx = with(density) { 210.dp.roundToPx() }
    val padHPx = with(density) { 24.dp.toPx() }
    val padVPx = with(density) { 18.dp.toPx() }
    val rankGapPx = with(density) { 58.dp.toPx() }
    val nodeGapPx = with(density) { 20.dp.toPx() }

    val layout = remember(graph, density.density, density.fontScale) {
        computeGraphLayout(
            graph = graph,
            rankGap = rankGapPx,
            nodeGap = nodeGapPx,
            pad = padHPx,
            nodeSize = { n ->
                if (n.shape == NodeShape.START_END) {
                    val d = with(density) { 24.dp.toPx() }
                    d to d
                } else {
                    val m = measurer.measure(n.label, style = labelStyle, constraints = Constraints(maxWidth = maxLabelPx))
                    val detailWidths = n.details.map {
                        measurer.measure(it, style = detailStyle, constraints = Constraints(maxWidth = maxLabelPx)).size.width
                    }
                    val contentW = max(m.size.width, detailWidths.maxOrNull() ?: 0).toFloat()
                    val detailH = n.details.size * with(density) { 14.dp.toPx() }
                    val extra = if (n.shape == NodeShape.DIAMOND) 1.35f else 1f
                    val w = (contentW * extra + padHPx).coerceAtLeast(with(density) { 76.dp.toPx() })
                    val h = ((m.size.height + detailH) * extra + padVPx).coerceAtLeast(with(density) { 40.dp.toPx() })
                    w to h
                }
            },
        )
    }

    val wDp = with(density) { layout.width.toDp() }
    val hDp = with(density) { layout.height.toDp() }
    val nodeBg = cs.surfaceContainerHigh
    val nodeBorder = cs.primary.copy(alpha = 0.72f)
    val textColor = cs.onSurface
    val edgeColor = cs.onSurfaceVariant.copy(alpha = 0.85f)
    val sgBg = cs.primary.copy(alpha = 0.06f)
    val sgBorder = cs.outlineVariant

    Canvas(Modifier.size(wDp, hDp)) {
        // 1. Subgraph containers
        for (sg in graph.subgraphs) {
            val pts = sg.nodeIds.mapNotNull { layout.nodes[it] }
            if (pts.isEmpty()) continue
            val minX = pts.minOf { it.x } - 14f
            val minY = pts.minOf { it.y } - 28f
            val maxX = pts.maxOf { it.x + it.w } + 14f
            val maxY = pts.maxOf { it.y + it.h } + 14f
            drawRoundRect(
                color = sgBg,
                topLeft = Offset(minX, minY),
                size = Size(maxX - minX, maxY - minY),
                cornerRadius = CornerRadius(14f, 14f),
            )
            drawRoundRect(
                color = sgBorder,
                topLeft = Offset(minX, minY),
                size = Size(maxX - minX, maxY - minY),
                cornerRadius = CornerRadius(14f, 14f),
                style = Stroke(width = 2f, pathEffect = PathEffect.dashPathEffect(floatArrayOf(10f, 6f))),
            )
            if (sg.title.isNotBlank()) {
                val tm = measurer.measure(sg.title, style = edgeLabelStyle.copy(color = ex.muted, fontWeight = FontWeight.SemiBold))
                drawText(tm, topLeft = Offset(minX + 10f, minY + 6f))
            }
        }

        // 2. Edges
        val horizontal = graph.direction == DiagramDirection.LR || graph.direction == DiagramDirection.RL
        for (e in graph.edges) {
            val u = layout.nodes[e.from] ?: continue
            val v = layout.nodes[e.to] ?: continue
            val (start, end) = if (horizontal) {
                if (u.x <= v.x) {
                    Offset(u.x + u.w, u.y + u.h / 2f) to Offset(v.x, v.y + v.h / 2f)
                } else {
                    Offset(u.x, u.y + u.h / 2f) to Offset(v.x + v.w, v.y + v.h / 2f)
                }
            } else {
                if (u.y <= v.y) {
                    Offset(u.x + u.w / 2f, u.y + u.h) to Offset(v.x + v.w / 2f, v.y)
                } else {
                    Offset(u.x + u.w / 2f, u.y) to Offset(v.x + v.w / 2f, v.y + v.h)
                }
            }

            val path = Path().apply {
                moveTo(start.x, start.y)
                if (horizontal) {
                    val midX = (start.x + end.x) / 2f
                    cubicTo(midX, start.y, midX, end.y, end.x, end.y)
                } else {
                    val midY = (start.y + end.y) / 2f
                    cubicTo(start.x, midY, end.x, midY, end.x, end.y)
                }
            }
            val strokeW = if (e.kind == EdgeKind.THICK) 4.2f else 2.6f
            val dash = if (e.kind == EdgeKind.DOTTED) PathEffect.dashPathEffect(floatArrayOf(9f, 7f)) else null
            drawPath(path, color = edgeColor, style = Stroke(width = strokeW, pathEffect = dash, cap = StrokeCap.Round))

            if (e.arrow) {
                val ctrl = if (horizontal) Offset((start.x + end.x) / 2f, end.y) else Offset(end.x, (start.y + end.y) / 2f)
                drawArrowHead(ctrl, end, edgeColor)
            }
            if (e.label.isNotBlank()) {
                val lm = measurer.measure(e.label, style = edgeLabelStyle.copy(color = textColor))
                val mid = Offset((start.x + end.x) / 2f - lm.size.width / 2f, (start.y + end.y) / 2f - lm.size.height / 2f)
                drawRoundRect(
                    color = cs.surfaceContainerHighest,
                    topLeft = Offset(mid.x - 6f, mid.y - 3f),
                    size = Size(lm.size.width + 12f, lm.size.height + 6f),
                    cornerRadius = CornerRadius(8f, 8f),
                )
                drawText(lm, topLeft = mid)
            }
        }

        // 3. Nodes
        for (pn in layout.nodes.values) {
            drawMermaidNode(
                pn = pn,
                measurer = measurer,
                maxLabelPx = maxLabelPx,
                labelStyle = labelStyle.copy(color = textColor),
                detailStyle = detailStyle.copy(color = ex.muted),
                bg = nodeBg,
                border = nodeBorder,
            )
        }
    }
}

private fun DrawScope.drawArrowHead(from: Offset, to: Offset, color: Color, len: Float = 12f) {
    val angle = atan2(to.y - from.y, to.x - from.x)
    val a1 = angle - Math.toRadians(28.0).toFloat()
    val a2 = angle + Math.toRadians(28.0).toFloat()
    val p = Path().apply {
        moveTo(to.x, to.y)
        lineTo(to.x - len * cos(a1), to.y - len * sin(a1))
        lineTo(to.x - len * cos(a2), to.y - len * sin(a2))
        close()
    }
    drawPath(p, color = color)
}

private fun DrawScope.drawMermaidNode(
    pn: PositionedNode,
    measurer: TextMeasurer,
    maxLabelPx: Int,
    labelStyle: TextStyle,
    detailStyle: TextStyle,
    bg: Color,
    border: Color,
) {
    val n = pn.node
    val topLeft = Offset(pn.x, pn.y)
    val sz = Size(pn.w, pn.h)
    when (n.shape) {
        NodeShape.START_END -> {
            drawCircle(color = border, radius = pn.w / 2f, center = Offset(pn.x + pn.w / 2f, pn.y + pn.h / 2f))
            return
        }
        NodeShape.CIRCLE -> {
            val r = min(pn.w, pn.h) / 2f
            val c = Offset(pn.x + pn.w / 2f, pn.y + pn.h / 2f)
            drawCircle(color = bg, radius = r, center = c)
            drawCircle(color = border, radius = r, center = c, style = Stroke(width = 2.5f))
        }
        NodeShape.DIAMOND -> {
            val cx = pn.x + pn.w / 2f
            val cy = pn.y + pn.h / 2f
            val p = Path().apply {
                moveTo(cx, pn.y)
                lineTo(pn.x + pn.w, cy)
                lineTo(cx, pn.y + pn.h)
                lineTo(pn.x, cy)
                close()
            }
            drawPath(p, color = bg)
            drawPath(p, color = border, style = Stroke(width = 2.5f))
        }
        else -> {
            val radius = when (n.shape) {
                NodeShape.RECT, NodeShape.SUBROUTINE -> 6f
                NodeShape.STADIUM -> pn.h / 2f
                else -> 14f
            }
            drawRoundRect(color = bg, topLeft = topLeft, size = sz, cornerRadius = CornerRadius(radius, radius))
            drawRoundRect(color = border, topLeft = topLeft, size = sz, cornerRadius = CornerRadius(radius, radius), style = Stroke(width = 2.4f))
            if (n.shape == NodeShape.SUBROUTINE) {
                drawLine(border, Offset(pn.x + 8f, pn.y), Offset(pn.x + 8f, pn.y + pn.h), strokeWidth = 2f)
                drawLine(border, Offset(pn.x + pn.w - 8f, pn.y), Offset(pn.x + pn.w - 8f, pn.y + pn.h), strokeWidth = 2f)
            }
        }
    }

    val m = measurer.measure(n.label, style = labelStyle, constraints = Constraints(maxWidth = maxLabelPx))
    if (n.details.isEmpty()) {
        drawText(
            m,
            topLeft = Offset(pn.x + (pn.w - m.size.width) / 2f, pn.y + (pn.h - m.size.height) / 2f),
        )
    } else {
        var curY = pn.y + 8f
        drawText(m, topLeft = Offset(pn.x + (pn.w - m.size.width) / 2f, curY))
        curY += m.size.height + 6f
        drawLine(border.copy(alpha = 0.4f), Offset(pn.x + 6f, curY), Offset(pn.x + pn.w - 6f, curY), strokeWidth = 1.5f)
        curY += 4f
        for (d in n.details) {
            val dm = measurer.measure(d, style = detailStyle, constraints = Constraints(maxWidth = maxLabelPx))
            drawText(dm, topLeft = Offset(pn.x + 10f, curY))
            curY += dm.size.height + 2f
        }
    }
}

@Composable
private fun SequenceDiagramCanvas(seq: MermaidAst.Sequence) {
    val measurer = rememberTextMeasurer()
    val density = LocalDensity.current
    val cs = MaterialTheme.colorScheme
    val ex = LocalExtra.current
    val actorStyle = MaterialTheme.typography.labelMedium.copy(color = cs.onSurface, fontWeight = FontWeight.SemiBold)
    val msgStyle = MaterialTheme.typography.labelSmall.copy(color = cs.onSurface, fontSize = 11.sp)

    val colW = with(density) { 150.dp.toPx() }
    val rowH = with(density) { 46.dp.toPx() }
    val pad = with(density) { 20.dp.toPx() }
    val headerH = with(density) { 38.dp.toPx() }
    val totalW = max(with(density) { 240.dp.toPx() }, seq.actors.size * colW + pad * 2)
    val totalH = headerH + pad * 2 + (seq.events.size + 1) * rowH

    Canvas(Modifier.size(with(density) { totalW.toDp() }, with(density) { totalH.toDp() })) {
        val centers = HashMap<String, Float>()
        seq.actors.forEachIndexed { i, a ->
            val cx = pad + colW * i + colW / 2f
            centers[a.id] = cx
            val boxW = colW * 0.82f
            drawLine(
                color = cs.outlineVariant,
                start = Offset(cx, pad + headerH),
                end = Offset(cx, totalH - pad),
                strokeWidth = 2f,
                pathEffect = PathEffect.dashPathEffect(floatArrayOf(8f, 6f)),
            )
            drawRoundRect(
                color = cs.surfaceContainerHigh,
                topLeft = Offset(cx - boxW / 2f, pad),
                size = Size(boxW, headerH),
                cornerRadius = CornerRadius(10f, 10f),
            )
            drawRoundRect(
                color = cs.primary.copy(alpha = 0.7f),
                topLeft = Offset(cx - boxW / 2f, pad),
                size = Size(boxW, headerH),
                cornerRadius = CornerRadius(10f, 10f),
                style = Stroke(width = 2.2f),
            )
            val tm = measurer.measure(a.label, style = actorStyle, constraints = Constraints(maxWidth = (boxW - 12f).toInt().coerceAtLeast(40)))
            drawText(tm, topLeft = Offset(cx - tm.size.width / 2f, pad + (headerH - tm.size.height) / 2f))
        }

        var y = pad + headerH + rowH * 0.75f
        for (ev in seq.events) {
            val x1 = centers[ev.from] ?: continue
            val x2 = centers[ev.to] ?: x1
            if (ev.isNote) {
                val left = min(x1, x2) - 44f
                val right = max(x1, x2) + 44f
                val tm = measurer.measure(ev.text, style = msgStyle, constraints = Constraints(maxWidth = (right - left - 16f).toInt().coerceAtLeast(80)))
                val h = max(28f, tm.size.height + 12f)
                drawRoundRect(ex.warning.copy(alpha = 0.16f), Offset(left, y - h / 2f), Size(right - left, h), CornerRadius(8f, 8f))
                drawText(tm, topLeft = Offset((left + right - tm.size.width) / 2f, y - tm.size.height / 2f))
            } else if (x1 == x2) {
                val loopW = 36f
                val p = Path().apply {
                    moveTo(x1, y - 8f)
                    lineTo(x1 + loopW, y - 8f)
                    lineTo(x1 + loopW, y + 10f)
                    lineTo(x1, y + 10f)
                }
                drawPath(p, color = cs.onSurfaceVariant, style = Stroke(width = 2.2f))
                drawArrowHead(Offset(x1 + loopW, y + 10f), Offset(x1, y + 10f), cs.onSurfaceVariant)
                if (ev.text.isNotBlank()) {
                    val tm = measurer.measure(ev.text, style = msgStyle)
                    drawText(tm, topLeft = Offset(x1 + loopW + 6f, y - tm.size.height / 2f))
                }
            } else {
                val dash = if (ev.dashed) PathEffect.dashPathEffect(floatArrayOf(8f, 6f)) else null
                drawLine(cs.onSurfaceVariant, Offset(x1, y), Offset(x2, y), strokeWidth = 2.4f, pathEffect = dash)
                drawArrowHead(Offset(x1, y), Offset(x2, y), cs.onSurfaceVariant)
                if (ev.text.isNotBlank()) {
                    val tm = measurer.measure(ev.text, style = msgStyle, constraints = Constraints(maxWidth = (kotlin.math.abs(x2 - x1) + 60f).toInt()))
                    drawText(tm, topLeft = Offset((x1 + x2 - tm.size.width) / 2f, y - tm.size.height - 4f))
                }
            }
            y += rowH
        }
    }
}

@Composable
private fun XyChartCanvas(chart: MermaidAst.XyChart) {
    val measurer = rememberTextMeasurer()
    val density = LocalDensity.current
    val cs = MaterialTheme.colorScheme
    val ex = LocalExtra.current
    val lblStyle = MaterialTheme.typography.labelSmall.copy(color = ex.muted, fontSize = 10.sp)
    val titleStyle = MaterialTheme.typography.labelMedium.copy(color = cs.onSurface, fontWeight = FontWeight.SemiBold)

    val allVals = chart.series.flatMap { it.values }
    val minV = chart.yMin ?: min(0f, allVals.minOrNull() ?: 0f)
    val maxV = (chart.yMax ?: (allVals.maxOrNull() ?: 1f)).let { if (it <= minV) minV + 1f else it }
    val count = max(chart.xLabels.size, chart.series.maxOfOrNull { it.values.size } ?: 1)
    val wDp = max(300, count * 54 + 72).dp
    val hDp = 220.dp

    Canvas(Modifier.size(wDp, hDp)) {
        val left = 48f
        val right = size.width - 20f
        val top = if (chart.title.isNotBlank()) 32f else 16f
        val bottom = size.height - 32f
        val plotW = (right - left).coerceAtLeast(60f)
        val plotH = (bottom - top).coerceAtLeast(60f)

        if (chart.title.isNotBlank()) {
            val tm = measurer.measure(chart.title, style = titleStyle)
            drawText(tm, topLeft = Offset((size.width - tm.size.width) / 2f, 6f))
        }

        // Gridlines
        for (i in 0..4) {
            val frac = i / 4f
            val y = bottom - frac * plotH
            drawLine(cs.outlineVariant.copy(alpha = 0.45f), Offset(left, y), Offset(right, y), strokeWidth = 1.2f)
            val v = minV + frac * (maxV - minV)
            val ym = measurer.measure(if (v % 1f == 0f) "${v.toInt()}" else "%.1f".format(v), style = lblStyle)
            drawText(ym, topLeft = Offset((left - ym.size.width - 6f).coerceAtLeast(2f), y - ym.size.height / 2f))
        }

        val stepW = plotW / count.coerceAtLeast(1)
        chart.xLabels.forEachIndexed { i, lbl ->
            val cx = left + stepW * (i + 0.5f)
            val xm = measurer.measure(lbl, style = lblStyle)
            drawText(xm, topLeft = Offset(cx - xm.size.width / 2f, bottom + 6f))
        }

        for (s in chart.series) {
            if (s.kind == "bar") {
                val barW = (stepW * 0.56f).coerceAtLeast(8f)
                s.values.forEachIndexed { i, v ->
                    val cx = left + stepW * (i + 0.5f)
                    val h = ((v - minV) / (maxV - minV)).coerceIn(0f, 1f) * plotH
                    drawRoundRect(
                        color = cs.primary.copy(alpha = 0.78f),
                        topLeft = Offset(cx - barW / 2f, bottom - h),
                        size = Size(barW, h),
                        cornerRadius = CornerRadius(6f, 6f),
                    )
                }
            } else {
                val p = Path()
                s.values.forEachIndexed { i, v ->
                    val cx = left + stepW * (i + 0.5f)
                    val cy = bottom - ((v - minV) / (maxV - minV)).coerceIn(0f, 1f) * plotH
                    if (i == 0) p.moveTo(cx, cy) else p.lineTo(cx, cy)
                    drawCircle(cs.secondary, radius = 5f, center = Offset(cx, cy))
                }
                if (s.values.size >= 2) {
                    drawPath(p, color = cs.secondary, style = Stroke(width = 3f, cap = StrokeCap.Round))
                }
            }
        }
    }
}
