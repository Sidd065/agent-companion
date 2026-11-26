package io.github.sidd065.agentcompanion.ui.chat

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.InsertDriveFile
import androidx.compose.material.icons.outlined.Build
import androidx.compose.material.icons.outlined.Cancel
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.DriveFileMove
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.ExpandLess
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.HelpOutline
import androidx.compose.material.icons.outlined.Lightbulb
import androidx.compose.material.icons.outlined.Psychology
import androidx.compose.material.icons.outlined.Public
import androidx.compose.material.icons.outlined.Schedule
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.Shield
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.TaskAlt
import androidx.compose.material.icons.outlined.RadioButtonUnchecked
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.runtime.collectAsState
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.style.TextDecoration
import io.github.sidd065.agentcompanion.graph
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import io.github.sidd065.agentcompanion.core.AppJson
import io.github.sidd065.agentcompanion.core.Item
import io.github.sidd065.agentcompanion.core.Perm
import io.github.sidd065.agentcompanion.core.PermOption
import io.github.sidd065.agentcompanion.core.Tool
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.components.checkerboard
import io.github.sidd065.agentcompanion.ui.components.markdownToPlain
import io.github.sidd065.agentcompanion.ui.components.CodeBox
import io.github.sidd065.agentcompanion.ui.components.DiffView
import io.github.sidd065.agentcompanion.ui.components.LineDiff
import io.github.sidd065.agentcompanion.ui.components.MarkdownText
import io.github.sidd065.agentcompanion.ui.components.Pill
import io.github.sidd065.agentcompanion.ui.components.baseName
import io.github.sidd065.agentcompanion.ui.components.fmtDuration
import io.github.sidd065.agentcompanion.ui.components.fmtTokens
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.put

// ---------- helpers over tool JSON ----------

fun JsonElement?.obj(): JsonObject? = this as? JsonObject
fun JsonElement?.str(key: String): String? = (this as? JsonObject)?.get(key)?.let { (it as? JsonPrimitive)?.contentOrNull }
fun JsonElement?.arr(): JsonArray? = this as? JsonArray

val prettyJson = kotlinx.serialization.json.Json { prettyPrint = true }

fun pretty(e: JsonElement?): String = if (e == null || e is JsonNull) "" else runCatching { prettyJson.encodeToString(JsonElement.serializer(), e) }.getOrDefault(e.toString())

fun Tool.isRunning(): Boolean = status == "in_progress" || status == "pending"

fun Tool.isPlanArtifact(): Boolean =
    input.obj()?.get("ArtifactMetadata").obj()?.get("RequestFeedback")?.let { (it as? JsonPrimitive)?.booleanOrNull } == true

fun Tool.command(): String? = input.str("CommandLine") ?: input.str("command") ?: input.str("cmd")

/** Splits agy's run_command output into (exit code, body). */
fun Tool.commandOutput(): Pair<Int?, String> {
    val raw = output.str("output") ?: content.arr()?.firstNotNullOfOrNull { c ->
        c.obj()?.get("content").str("text")
    } ?: ""
    val exit = output.obj()?.get("exit_code")?.let { (it as? JsonPrimitive)?.intOrNull }
        ?: output.obj()?.get("exitCode")?.let { (it as? JsonPrimitive)?.intOrNull }
        ?: Regex("Command exited with code (-?\\d+)").find(raw)?.groupValues?.get(1)?.toIntOrNull()
    val body = raw.substringAfter("Command output:\n", raw).replace("\r\n", "\n").trimEnd()
    return exit to body
}

/** One file's change. agentc trims big diffs to the changed lines; [startLine] is where the kept text starts in the file. */
data class FileDiff(val path: String, val old: String?, val new: String?, val startLine: Int = 1)

fun Tool.diffs(): List<FileDiff> = content.arr()?.mapNotNull { c ->
    val o = c.obj() ?: return@mapNotNull null
    if (o.str("type") != "diff") return@mapNotNull null
    FileDiff(o.str("path") ?: "", o.str("oldText"), o.str("newText"), (o["line"] as? JsonPrimitive)?.intOrNull?.coerceAtLeast(1) ?: 1)
} ?: emptyList()

/** The diff's lines, numbered as in the file. */
fun FileDiff.lines(): List<io.github.sidd065.agentcompanion.ui.components.DiffLine> {
    val lines = LineDiff.diff(old, new)
    val k = startLine - 1
    if (k <= 0) return lines
    return lines.map { it.copy(oldNo = if (it.oldNo > 0) it.oldNo + k else 0, newNo = if (it.newNo > 0) it.newNo + k else 0) }
}

fun Tool.path(): String? = locations.arr()?.firstOrNull().str("path") ?: input.str("Path") ?: input.str("path") ?: input.str("AbsolutePath") ?: input.str("file_path")

/** Claude's structured patch for a finished edit, as diff lines with real line numbers. */
fun Tool.patchLines(): List<io.github.sidd065.agentcompanion.ui.components.DiffLine>? {
    val hunks = output.obj()?.get("structuredPatch").arr()?.takeIf { it.isNotEmpty() } ?: return null
    val out = ArrayList<io.github.sidd065.agentcompanion.ui.components.DiffLine>()
    hunks.forEachIndexed { hi, h ->
        val o = h.obj() ?: return@forEachIndexed
        var oldNo = (o["oldStart"] as? JsonPrimitive)?.intOrNull ?: 1
        var newNo = (o["newStart"] as? JsonPrimitive)?.intOrNull ?: 1
        if (hi > 0) out.add(io.github.sidd065.agentcompanion.ui.components.DiffLine(' ', "⋯", 0, 0))
        o["lines"].arr()?.forEach { l ->
            val s = (l as? JsonPrimitive)?.contentOrNull ?: return@forEach
            when (s.firstOrNull()) {
                '+' -> { out.add(io.github.sidd065.agentcompanion.ui.components.DiffLine('+', s.drop(1), 0, newNo)); newNo++ }
                '-' -> { out.add(io.github.sidd065.agentcompanion.ui.components.DiffLine('-', s.drop(1), oldNo, 0)); oldNo++ }
                '\\' -> {}
                else -> { out.add(io.github.sidd065.agentcompanion.ui.components.DiffLine(' ', s.drop(1), oldNo, newNo)); oldNo++; newNo++ }
            }
        }
    }
    return out
}

private val claudeToolIcons = mapOf(
    "AskUserQuestion" to Icons.Outlined.HelpOutline, "ExitPlanMode" to Icons.Outlined.TaskAlt, "EnterPlanMode" to Icons.Outlined.TaskAlt,
    "Agent" to Icons.Outlined.Psychology, "Task" to Icons.Outlined.Psychology, "Skill" to Icons.Outlined.Lightbulb,
    "WebSearch" to Icons.Outlined.Search, "CronCreate" to Icons.Outlined.Schedule, "ScheduleWakeup" to Icons.Outlined.Schedule,
)

private val kindIcon = mapOf(
    "execute" to Icons.Outlined.Terminal, "edit" to Icons.Outlined.Edit, "read" to Icons.Outlined.Description,
    "search" to Icons.Outlined.Search, "fetch" to Icons.Outlined.Public, "think" to Icons.Outlined.Psychology,
    "delete" to Icons.Outlined.Delete, "move" to Icons.Outlined.DriveFileMove,
)

fun toolIcon(t: Tool): ImageVector = when (t.name) {
    "ask_question" -> Icons.Outlined.HelpOutline
    "schedule", "manage_task" -> Icons.Outlined.Schedule
    else -> claudeToolIcons[t.name] ?: kindIcon[t.kind] ?: Icons.Outlined.Build
}

/** Pretty name for an agent tool without a dedicated verb (MCP tools: "server · tool"). */
fun toolDisplayName(name: String?): String {
    val n = name ?: return "Tool"
    if (n.startsWith("mcp__")) {
        val rest = n.removePrefix("mcp__")
        val i = rest.indexOf("__")
        return if (i < 0) rest else rest.substring(0, i).removePrefix("claude_ai_").replace('_', ' ') + " · " + rest.substring(i + 2).replace('_', ' ')
    }
    if (n.any { it == '_' }) return n.replace('_', ' ').replaceFirstChar { it.uppercase() }
    // CamelCase → words
    return n.replace(Regex("(?<=[a-z])(?=[A-Z])"), " ")
}

fun toolVerb(t: Tool): String {
    val running = t.isRunning()
    return when {
        t.name == "write_file" || t.name == "write_to_file" || t.name == "Write" -> if (running) "Writing" else "Wrote"
        t.name == "Agent" || t.name == "Task" -> if (running) "Agent working" else "Agent"
        t.name == "AskUserQuestion" -> "Asked"
        t.name == "ExitPlanMode" -> when (t.status) { "completed" -> "Plan approved"; "failed" -> "Plan sent back"; else -> "Plan" }
        t.name == "EnterPlanMode" -> "Entered plan mode"
        t.name == "WebSearch" -> if (running) "Searching the web" else "Searched the web"
        t.name == "Skill" -> if (running) "Using skill" else "Used skill"
        t.name == "ToolSearch" -> "Loaded tools"
        t.name?.startsWith("mcp__") == true -> toolDisplayName(t.name)
        t.kind == "execute" -> if (running) "Running" else "Ran"
        t.kind == "edit" -> if (running) "Editing" else "Edited"
        t.kind == "read" -> if (running) "Reading" else "Read"
        t.kind == "search" -> if (running) "Searching" else "Searched"
        t.kind == "fetch" -> if (running) "Fetching" else "Fetched"
        t.kind == "delete" -> if (running) "Deleting" else "Deleted"
        t.kind == "move" -> if (running) "Moving" else "Moved"
        t.kind == "think" -> "Thought"
        else -> toolDisplayName(t.name)
    }
}

fun toolTitle(t: Tool): String {
    val title = t.title.orEmpty()
    return when {
        t.name?.startsWith("mcp__") == true -> title.substringAfter(" · ").substringAfter(" ", "").ifBlank { "" }
        t.name == "ExitPlanMode" -> t.input.str("plan")?.lineSequence()?.firstOrNull { it.isNotBlank() }?.trim()?.trimStart('#', ' ') ?: title
        t.kind == "execute" -> t.command() ?: title
        (t.kind == "edit" || t.kind == "read") && t.path() != null -> {
            val extra = (t.diffs().size - 1).coerceAtLeast(0)
            baseName(t.path()!!) + if (t.kind == "edit" && extra > 0) " (+$extra more)" else ""
        }
        title.startsWith("File ") -> title.removePrefix("File ")
        title.startsWith("Run ") -> title.removePrefix("Run ")
        else -> title
    }
}

fun summarize(tools: List<Tool>): String {
    val parts = mutableListOf<String>()
    fun add(n: Int, one: String, many: String) { if (n > 0) parts.add(if (n == 1) one else "$n $many") }
    add(tools.count { it.kind == "execute" }, "ran a command", "commands")
    add(tools.count { it.kind == "edit" }, "edited a file", "files edited")
    add(tools.count { it.kind == "read" }, "read a file", "files read")
    add(tools.count { it.kind == "search" }, "searched", "searches")
    add(tools.count { it.kind == "fetch" }, "fetched a page", "pages fetched")
    val other = tools.count { it.kind !in setOf("execute", "edit", "read", "search", "fetch") }
    add(other, "used a tool", "other tools")
    return parts.joinToString(", ").replaceFirstChar { it.uppercase() }
}

// ---------- display rows ----------

sealed interface Row {
    val key: String
    data class Single(val item: Item) : Row { override val key get() = item.id }
    data class Tools(val items: List<Item>) : Row { override val key get() = "g:" + items.first().id }
}

/** Groups consecutive tool calls (3+) into one collapsible row. */
fun buildRows(items: List<Item>, showThoughts: Boolean): List<Row> {
    val out = ArrayList<Row>()
    val run = ArrayList<Item>()
    // Claude's question/plan requests are answered on their own cards: the
    // tool rows they come from would only repeat them.
    val carded = items.filter { it.kind == "perm" && (it.perm?.kind == "question" || it.perm?.kind == "plan") }.mapNotNull { it.perm?.callId }.toSet()
    fun flush() {
        if (run.size >= 3) out.add(Row.Tools(run.toList())) else run.forEach { out.add(Row.Single(it)) }
        run.clear()
    }
    for (it in items) {
        if (it.kind == "thought" && !showThoughts) continue
        if (it.kind == "tool" && it.tool?.callId in carded) continue
        val isAnsweredToolPerm = it.kind == "perm" && it.state != "pending" && it.perm != null &&
            (it.perm.kind.isNullOrBlank() || it.perm.kind == "tool")
        val groupable = (it.kind == "tool" && it.tool != null && it.tool.name != "ask_question" && !it.tool.isPlanArtifact() &&
            it.tool.name != "AskUserQuestion" && it.tool.name != "ExitPlanMode" && it.tool.name != "Agent" && it.tool.name != "Task") ||
            isAnsweredToolPerm
        if (groupable) run.add(it) else {
            flush()
            out.add(Row.Single(it))
        }
    }
    flush()
    return out
}

// ---------- row views ----------

class TranscriptActions(
    val conn: HostConnection,
    val sessionId: String,
    val onUserLong: (Item) -> Unit,
    val onAgentLong: (Item) -> Unit,
    val onSendText: (String) -> Unit,
    val onPrefill: (String) -> Unit,
    val onOpenFile: (String) -> Unit,
    val onQueueAction: (Item, String) -> Unit,
    val onApprovePlan: (switchMode: Boolean) -> Unit,
    /** Opens the live log of a running shell command (by its command line). */
    val onLiveOutput: ((String) -> Unit)? = null,
    /** The session's agent (agy|claude|codex): wording and answer formats differ. */
    val agent: String = "agy",
    /** The tool call a permission request is about (for its diff). */
    val toolFor: (String) -> io.github.sidd065.agentcompanion.core.Tool? = { null },
    /** Open a turn-scoped checkpoint diff dialog. */
    val onCheckpointDiff: ((Int) -> Unit)? = null,
    /** Revert workspace to before a turn checkpoint. */
    val onCheckpointRevert: ((Int) -> Unit)? = null,
) {
    val label: String get() = io.github.sidd065.agentcompanion.core.agentLabel(agent)
    val isClaude: Boolean get() = agent == "claude"
    val isCodex: Boolean get() = agent == "codex"
}

internal fun permOptionLabel(o: PermOption, isClaude: Boolean, isCodex: Boolean): String =
    if ((isClaude || isCodex) && o.name.isNotBlank()) {
        o.name
    } else {
        when (o.kind) {
            "allow_once" -> "Allow once"
            "allow_always" -> "Always allow"
            "reject_once" -> "Deny"
            "reject_always" -> "Always deny"
            else -> o.name
        }
    }

@Composable
fun TranscriptRow(row: Row, actions: TranscriptActions, isLast: Boolean, activeId: String?, wrap: Boolean, cwd: String) {
    when (row) {
        is Row.Tools -> ToolGroup(row.items, actions, defaultOpen = isLast && row.items.any { it.tool?.isRunning() == true }, wrap = wrap, cwd = cwd)
        is Row.Single -> {
            val it = row.item
            when (it.kind) {
                "user" -> UserBubble(it, actions)
                "agent" -> AgentMessage(it, actions)
                "thought" -> ThoughtBlock(it)
                "tool" -> {
                    val t = it.tool!!
                    when {
                        t.name == "ask_question" -> QuestionCard(it, active = it.id == activeId, actions = actions)
                        t.isPlanArtifact() -> PlanReviewCard(it, active = it.id == activeId, actions = actions)
                        else -> ToolRow(it, actions, wrap, cwd, indent = false)
                    }
                }
                "perm" -> when (it.perm?.kind) {
                    "question" -> ClaudeQuestionCard(it, actions)
                    "plan" -> ClaudePlanCard(it, actions)
                    else -> PermCard(it, actions)
                }
                "plan" -> TodoCard(it)
                "notice" -> NoticeRow(it)
                "turn" -> TurnRow(it, actions)
            }
        }
    }
}

/** The question/plan card that is waiting for the user (latest one, no user message after it). */
fun activeAttentionId(items: List<Item>, attention: String?): String? {
    if (attention != "plan" && attention != "question") return null
    val lastUser = items.indexOfLast { it.kind == "user" }
    for (i in items.indices.reversed()) {
        if (i <= lastUser) break
        val t = items[i].tool ?: continue
        if (attention == "plan" && t.isPlanArtifact()) return items[i].id
        if (attention == "question" && t.name == "ask_question") return items[i].id
    }
    return null
}

private val spaces = Regex("\\s+")
private val inlineUrlRegex = Regex("(?:https?|file)://[^\\s<>\"]+")

fun linkifyBubbleText(raw: String, linkColor: androidx.compose.ui.graphics.Color, onOpenUri: (String) -> Unit): androidx.compose.ui.text.AnnotatedString = buildAnnotatedString {
    var pos = 0
    for (m in inlineUrlRegex.findAll(raw)) {
        var end = m.range.last + 1
        while (end > m.range.first && raw[end - 1] in charArrayOf('.', ',', ')', ']', ';', ':')) {
            end--
        }
        val url = raw.substring(m.range.first, end)
        append(raw.substring(pos, m.range.first))
        pushLink(
            LinkAnnotation.Url(
                url = url,
                styles = TextLinkStyles(style = SpanStyle(color = linkColor, textDecoration = TextDecoration.Underline)),
                linkInteractionListener = { onOpenUri(url) },
            )
        )
        append(url)
        pop()
        pos = end
    }
    if (pos < raw.length) append(raw.substring(pos))
}

/** Markdown-ish text flattened for one-line previews. */
fun plainPreview(s: String): String = markdownToPlain(s).replace(spaces, " ").trim()

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun UserBubble(item: Item, actions: TranscriptActions) {
    val ex = LocalExtra.current
    Column(Modifier.fillMaxWidth().padding(start = 48.dp, end = 12.dp, top = 10.dp, bottom = 4.dp), horizontalAlignment = Alignment.End) {
        Surface(
            color = ex.userBubble, shape = RoundedCornerShape(18.dp, 18.dp, 4.dp, 18.dp),
            modifier = Modifier.combinedClickable(onClick = {}, onLongClick = { actions.onUserLong(item) })
                .alpha(if (item.state == "queued") 0.75f else 1f),
        ) {
            Column(Modifier.padding(horizontal = 14.dp, vertical = 10.dp)) {
                item.blocks?.filter { it.type == "image" }?.takeIf { it.isNotEmpty() }?.let { imgs ->
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        imgs.forEach { b ->
                            AsyncImage(
                                model = b.uri?.let { u -> if (u.startsWith("att:")) io.github.sidd065.agentcompanion.net.RemoteImage(actions.conn.config.id, attachment = u) else null },
                                contentDescription = b.name, contentScale = ContentScale.Crop,
                                modifier = Modifier.size(120.dp).clip(RoundedCornerShape(10.dp)).checkerboard(),
                            )
                        }
                    }
                    Spacer(Modifier.height(6.dp))
                }
                val fb = item.text?.let { parseFeedback(it) }
                if (fb != null) {
                    // Comments sent from the artifact / diff reviewer: show them compactly.
                    var full by remember { mutableStateOf(false) }
                    Column(Modifier.clickable { full = !full }) {
                        Text(if (fb.isEmpty()) "Approved" else "${fb.size} comment${if (fb.size > 1) "s" else ""}",
                            style = MaterialTheme.typography.labelMedium, color = ex.onUserBubble.copy(alpha = 0.7f))
                        if (full) Text(item.text, style = MaterialTheme.typography.bodySmall, color = ex.onUserBubble)
                        else fb.forEach { (file, c) ->
                            Text("$file: $c", style = MaterialTheme.typography.bodyMedium, color = ex.onUserBubble)
                        }
                    }
                } else if (!item.text.isNullOrBlank()) {
                    // A pasted log or review can run for screens: show the start until asked.
                    val long = item.text.length > 1500 || item.text.count { it == '\n' } > 24
                    var expanded by rememberSaveable(item.id) { mutableStateOf(false) }
                    val uriHandler = LocalUriHandler.current
                    val annotated = remember(item.text, ex.onUserBubble) {
                        linkifyBubbleText(item.text, ex.onUserBubble) { uriHandler.openUri(it) }
                    }
                    // Not selectable: a long press opens the message sheet (which has Select text).
                    Text(annotated, style = MaterialTheme.typography.bodyMedium, color = ex.onUserBubble,
                        maxLines = if (long && !expanded) 14 else Int.MAX_VALUE, overflow = TextOverflow.Ellipsis)
                    if (long) Text(
                        if (expanded) "Show less" else "Show all ${item.text.length / 1000}k characters",
                        style = MaterialTheme.typography.labelLarge, color = ex.onUserBubble.copy(alpha = 0.8f),
                        modifier = Modifier.padding(top = 6.dp).clip(RoundedCornerShape(6.dp)).clickable { expanded = !expanded }.padding(vertical = 2.dp),
                    )
                }
                item.blocks?.filter { it.type != "image" }?.forEach { b ->
                    Row(
                        Modifier.padding(top = 6.dp).clip(RoundedCornerShape(8.dp)).background(ex.onUserBubble.copy(alpha = 0.08f))
                            .clickable { b.uri?.removePrefix("file://")?.let(actions.onOpenFile) }.padding(horizontal = 8.dp, vertical = 4.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(Icons.AutoMirrored.Outlined.InsertDriveFile, null, Modifier.size(14.dp), tint = ex.onUserBubble)
                        Spacer(Modifier.width(4.dp))
                        Text(b.name ?: baseName(b.uri ?: ""), style = MaterialTheme.typography.labelMedium, color = ex.onUserBubble)
                    }
                }
            }
        }
        when (item.state) {
            "queued" -> Row(Modifier.padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Pill("Queued", ex.warning)
                TextButton(onClick = { actions.onQueueAction(item, "send") }) { Text("Send now") }
                TextButton(onClick = { actions.onQueueAction(item, "edit") }) { Text("Edit") }
                TextButton(onClick = { actions.onQueueAction(item, "remove") }) { Text("Remove") }
            }
            "sent" -> Text("Sending…", style = MaterialTheme.typography.labelSmall, color = ex.muted, modifier = Modifier.padding(top = 2.dp))
            "failed" -> Row(verticalAlignment = Alignment.CenterVertically) {
                Text("Not delivered", style = MaterialTheme.typography.labelSmall, color = ex.danger)
                TextButton(onClick = { item.text?.let(actions.onSendText) }) { Text("Retry") }
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun AgentMessage(item: Item, actions: TranscriptActions) {
    Box(Modifier.fillMaxWidth().combinedClickable(onClick = {}, onLongClick = { actions.onAgentLong(item) }).padding(horizontal = 16.dp, vertical = 6.dp)) {
        Column {
            item.blocks?.filter { it.type == "image" }?.forEach { b ->
                AsyncImage(model = b.uri?.let { u -> if (u.startsWith("att:")) io.github.sidd065.agentcompanion.net.RemoteImage(actions.conn.config.id, attachment = u) else null }, contentDescription = null,
                    modifier = Modifier.fillMaxWidth().heightIn(max = 320.dp).clip(RoundedCornerShape(10.dp)))
            }
            // Long-press opens the message sheet (Copy, Select text, Fork…); inline
            // selection would start at the same time and fight it.
            if (!item.text.isNullOrBlank()) {
                val trimmed = item.text.trim()
                if (trimmed.startsWith("⚠")) {
                    val ex = LocalExtra.current
                    Surface(
                        color = ex.danger.copy(alpha = 0.10f),
                        shape = RoundedCornerShape(12.dp),
                        border = androidx.compose.foundation.BorderStroke(1.dp, ex.danger.copy(alpha = 0.40f)),
                        modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
                    ) {
                        Box(Modifier.padding(12.dp)) {
                            MarkdownText(trimmed, selectable = false)
                        }
                    }
                } else {
                    MarkdownText(trimmed, selectable = false)
                }
            }
        }
    }
}

@Composable
fun ThoughtBlock(item: Item) {
    val ex = LocalExtra.current
    var open by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 2.dp)) {
        Row(Modifier.clip(RoundedCornerShape(8.dp)).clickable { open = !open }.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Outlined.Lightbulb, null, Modifier.size(15.dp), tint = ex.muted)
            Spacer(Modifier.width(6.dp))
            val head = item.text.orEmpty().lineSequence().firstOrNull { it.isNotBlank() }?.let { plainPreview(it) }?.take(80)?.ifBlank { null } ?: "Thinking"
            Text(head, style = MaterialTheme.typography.bodySmall, color = ex.muted, fontStyle = FontStyle.Italic, maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false))
            Icon(if (open) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore, null, Modifier.size(16.dp), tint = ex.muted)
        }
        AnimatedVisibility(open) {
            Box(Modifier.padding(start = 21.dp, bottom = 6.dp)) {
                // Thoughts are Markdown too (bold section titles, `code`, lists).
                MarkdownText(item.text.orEmpty().trim(), style = MaterialTheme.typography.bodySmall.copy(fontStyle = FontStyle.Italic),
                    color = ex.muted, selectable = false)
            }
        }
    }
}

@Composable
fun ToolGroup(items: List<Item>, actions: TranscriptActions, defaultOpen: Boolean, wrap: Boolean, cwd: String) {
    val ex = LocalExtra.current
    val expandDefault = LocalContext.current.graph.prefs.flow.collectAsState().value.expandToolCalls
    var open by remember(expandDefault) { mutableStateOf(defaultOpen || expandDefault) }
    val tools = items.mapNotNull { it.tool }
    val running = tools.any { it.isRunning() }
    val failed = tools.count { it.status == "failed" }
    Column(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 2.dp)) {
        Row(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { open = !open }.padding(horizontal = 4.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (running) CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
            else Icon(Icons.Outlined.Build, null, Modifier.size(15.dp), tint = ex.muted)
            Spacer(Modifier.width(8.dp))
            Text(summarize(tools) + if (failed > 0) " · $failed failed" else "", style = MaterialTheme.typography.bodySmall, color = ex.muted, modifier = Modifier.weight(1f))
            Text("${tools.size}", style = MaterialTheme.typography.labelSmall, color = ex.muted)
            Icon(if (open) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore, null, Modifier.size(16.dp), tint = ex.muted)
        }
        AnimatedVisibility(open) {
            Column {
                items.forEach {
                    if (it.kind == "perm") PermCard(it, actions) else ToolRow(it, actions, wrap, cwd, indent = true)
                }
            }
        }
    }
}

@Composable
fun ToolStatusIcon(t: Tool) {
    val ex = LocalExtra.current
    when {
        t.isRunning() -> CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
        t.status == "failed" -> Icon(Icons.Outlined.Cancel, "failed", Modifier.size(15.dp), tint = ex.danger)
        else -> Icon(Icons.Outlined.CheckCircle, "done", Modifier.size(15.dp), tint = ex.success.copy(alpha = 0.85f))
    }
}

@Composable
fun ToolRow(item: Item, actions: TranscriptActions, wrap: Boolean, cwd: String, indent: Boolean) {
    val t = item.tool ?: return
    val ex = LocalExtra.current
    val expandDefault = LocalContext.current.graph.prefs.flow.collectAsState().value.expandToolCalls
    var open by remember(expandDefault) { mutableStateOf(expandDefault) }
    var full by remember { mutableStateOf<Item?>(null) }
    val scope = rememberCoroutineScope()
    val shown = full?.tool ?: t
    val (added, removed) = remember(t) { t.diffs().fold(0 to 0) { acc, d -> val (a, r) = LineDiff.stats(LineDiff.diff(d.old, d.new)); (acc.first + a) to (acc.second + r) } }
    Column(Modifier.fillMaxWidth().padding(start = if (indent) 10.dp else 12.dp, end = 12.dp, top = 1.dp, bottom = 1.dp)) {
        Row(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { open = !open }.padding(horizontal = 4.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(toolIcon(t), null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.width(8.dp))
            Text(toolVerb(t), style = MaterialTheme.typography.bodySmall, fontWeight = FontWeight.SemiBold)
            Spacer(Modifier.width(6.dp))
            Text(
                toolTitle(t).let { s -> if (t.kind == "edit" || t.kind == "read") s.removePrefix("$cwd/") else s },
                style = MaterialTheme.typography.bodySmall.copy(fontFamily = androidx.compose.ui.text.font.FontFamily.Monospace),
                color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f),
            )
            if (added + removed > 0) {
                Text("+$added", style = MaterialTheme.typography.labelSmall, color = ex.diffAdd)
                Spacer(Modifier.width(3.dp))
                Text("−$removed", style = MaterialTheme.typography.labelSmall, color = ex.diffDel)
                Spacer(Modifier.width(6.dp))
            }
            if (t.endTs > 0 && t.startTs > 0 && t.endTs - t.startTs > 1500) {
                Text(fmtDuration(t.endTs - t.startTs), style = MaterialTheme.typography.labelSmall, color = ex.muted)
                Spacer(Modifier.width(6.dp))
            }
            ToolStatusIcon(t)
        }
        AnimatedVisibility(open) {
            Column(Modifier.padding(start = 24.dp, top = 2.dp, bottom = 8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                ToolDetails(shown, actions, wrap)
                if (item.trunc && full == null) {
                    TextButton(onClick = {
                        scope.launch {
                            runCatching {
                                full = actions.conn.rpc.call("session.item", params { put("id", actions.sessionId); put("itemId", item.id) }).decode(Item.serializer())
                            }
                        }
                    }) { Text("Load full output") }
                }
            }
        }
    }
}

@Composable
fun ToolDetails(t: Tool, actions: TranscriptActions, wrap: Boolean) {
    val ex = LocalExtra.current
    when {
        t.kind == "execute" -> {
            t.command()?.let { CodeBox("$ $it", wrap = wrap) }
            if (t.isRunning() && actions.onLiveOutput != null) {
                androidx.compose.material3.TextButton(onClick = { t.command()?.let { actions.onLiveOutput.invoke(it) } }) {
                    Icon(Icons.Outlined.Terminal, null, Modifier.size(16.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("Live output")
                }
            }
            val (exit, body) = t.commandOutput()
            if (exit != null) {
                Pill(if (exit == 0) "exit 0" else "exit $exit", if (exit == 0) ex.success else ex.danger)
            }
            if (body.isNotBlank()) CodeBox(body, wrap = wrap)
        }
        t.name == "ExitPlanMode" -> {
            t.input.str("plan")?.let { plan ->
                Box(Modifier.fillMaxWidth().heightIn(max = 420.dp).clip(RoundedCornerShape(8.dp)).border(1.dp, ex.cardBorder, RoundedCornerShape(8.dp)).verticalScroll(rememberScrollState()).padding(10.dp)) {
                    MarkdownText(plan, immediate = true)
                }
            }
            t.input.str("planFilePath")?.let { p -> TextButton(onClick = { actions.onOpenFile(p) }) { Text("Open plan") } }
        }
        t.name == "AskUserQuestion" -> {
            val answers = t.output.obj()?.get("answers").obj()
            t.input.obj()?.get("questions").arr()?.forEach { q ->
                val qt = q.str("question") ?: return@forEach
                Text(qt, style = MaterialTheme.typography.bodySmall)
                val a = answers?.get(qt)?.let { (it as? JsonPrimitive)?.contentOrNull }
                Text(if (a != null) "→ $a" else "(not answered)", style = MaterialTheme.typography.bodySmall, color = if (a != null) MaterialTheme.colorScheme.primary else ex.muted)
            }
        }
        (t.name == "Edit" || t.name == "Write" || t.name == "MultiEdit") && t.patchLines() != null -> {
            val p = t.path() ?: t.diffs().firstOrNull()?.path.orEmpty()
            Text(baseName(p), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary, modifier = Modifier.clickable { actions.onOpenFile(p) })
            DiffView(t.patchLines()!!, Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)), wrap = wrap, maxLines = 400)
        }
        t.name == "Agent" || t.name == "Task" -> {
            t.input.str("prompt")?.let { pr ->
                Text("Task", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                CodeBox(pr, wrap = true, maxChars = 6_000)
            }
            val report = t.content.arr()?.mapNotNull { c -> c.obj()?.get("content").str("text") }?.joinToString("\n")
            if (!report.isNullOrBlank()) {
                Text("Report", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                MarkdownText(report)
            }
            t.output.obj()?.let { o ->
                val parts = listOfNotNull(
                    (o["totalToolUseCount"] as? JsonPrimitive)?.intOrNull?.let { "$it tool calls" },
                    (o["totalTokens"] as? JsonPrimitive)?.contentOrNull?.toLongOrNull()?.let { "${fmtTokens(it)} tokens" },
                    (o["totalDurationMs"] as? JsonPrimitive)?.contentOrNull?.toLongOrNull()?.let { fmtDuration(it) },
                )
                if (parts.isNotEmpty()) Text(parts.joinToString(" · "), style = MaterialTheme.typography.labelSmall, color = ex.muted)
            }
        }
        t.diffs().isNotEmpty() -> {
            t.diffs().forEach { d ->
                Text(
                    baseName(d.path), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.clickable { actions.onOpenFile(d.path) },
                )
                DiffView(d.lines(), Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)), wrap = wrap, maxLines = 400)
            }
        }
        else -> {
            t.path()?.let { p ->
                Text(p, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary, modifier = Modifier.clickable { actions.onOpenFile(p) })
            }
            val inp = pretty(t.input)
            if (inp.isNotBlank() && inp != "{}") {
                Text("Input", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                CodeBox(inp, wrap = wrap, maxChars = 20_000)
            }
            val text = t.content.arr()?.mapNotNull { c -> c.obj()?.get("content").str("text") }?.joinToString("\n")
            val out = text?.takeIf { it.isNotBlank() } ?: pretty(t.output).takeIf { it.isNotBlank() && it != "{}" }
            if (out != null) {
                Text("Output", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                CodeBox(out, wrap = wrap, maxChars = 40_000)
            }
        }
    }
}

/** The full command a shell permission request would run, from its tool call's input. */
internal fun permCommand(p: Perm, actions: TranscriptActions): String? =
    if (p.toolKind != "execute") null
    else p.callId?.let(actions.toolFor)?.command()?.trimEnd()?.takeIf { it.isNotBlank() }

/** A command in a code box. Over [collapsedLines] lines it starts folded, with a toggle to show the rest. */
@Composable
fun CommandBox(text: String, collapsedLines: Int = 30) {
    val lines = remember(text) { text.lines() }
    val long = lines.size > collapsedLines + 4
    var all by remember(text) { mutableStateOf(false) }
    CodeBox(if (long && !all) lines.take(collapsedLines).joinToString("\n") else text, wrap = true)
    if (long) {
        androidx.compose.material3.TextButton(onClick = { all = !all }, contentPadding = PaddingValues(horizontal = 4.dp)) {
            Text(if (all) "Show less" else "Show all ${lines.size} lines")
        }
    }
}

@Composable
fun PermCard(item: Item, actions: TranscriptActions) {
    val p = item.perm ?: return
    val ex = LocalExtra.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    var denyNote by remember { mutableStateOf<String?>(null) }
    val pending = item.state == "pending"
    val who = if (p.subagent) "A subagent" else actions.label
    val verb = when (p.toolKind) {
        "execute" -> "$who wants to run:"
        "edit" -> "$who wants to edit:"
        "fetch" -> "$who wants to fetch:"
        "delete" -> "$who wants to delete:"
        "read" -> "$who wants to read:"
        "search" -> "$who wants to search:"
        else -> if (p.tool != null) "$who wants to use ${toolDisplayName(p.tool)}:" else "$who wants to use a tool:"
    }
    if (!pending) {
        // Answered: one line under the tool row it was about, the command a tap away.
        val chosen = p.options.firstOrNull { it.optionId == p.chosen }
        val allowed = chosen?.kind?.startsWith("allow") == true
        val status = when {
            item.state == "cancelled" -> "Cancelled"
            chosen == null -> "Answered"
            // Only add the option name when it says more than allow/deny (e.g. "Always allow").
            allowed -> if (chosen!!.name.trim().lowercase() in setOf("allow", "allow once", "yes")) "Allowed" else "Allowed · ${chosen.name}"
            else -> if (chosen!!.name.trim().lowercase() in setOf("deny", "reject", "no")) "Denied" else "Denied · ${chosen.name}"
        }
        val tint = if (allowed) ex.success else ex.muted
        var open by remember(item.id) { mutableStateOf(false) }
        Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 2.dp)) {
            Row(
                Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable { open = !open }.padding(vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.Outlined.Shield, null, Modifier.size(14.dp), tint = tint)
                Spacer(Modifier.width(6.dp))
                Text(status, style = MaterialTheme.typography.labelMedium, color = tint)
                Spacer(Modifier.width(8.dp))
                val compactTitle = remember(p.title) {
                    val raw = p.title.orEmpty().trim()
                    if (raw.startsWith("File ") && raw.contains('/')) {
                        val fp = raw.removePrefix("File ").trim()
                        val fname = fp.substringAfterLast('/')
                        val parent = fp.substringBeforeLast('/', "").substringAfterLast('/')
                        if (parent.isNotBlank()) "$fname · …/$parent" else fname
                    } else raw
                }
                Text(compactTitle, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = ex.muted,
                    maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            }
            if (open) CommandBox(permCommand(p, actions) ?: p.title ?: "tool call")
            p.answer.obj()?.get("message")?.let { (it as? JsonPrimitive)?.contentOrNull }?.takeIf { it.isNotBlank() }?.let { m ->
                Text("“$m”", style = MaterialTheme.typography.bodySmall, color = ex.muted, fontStyle = FontStyle.Italic, modifier = Modifier.padding(start = 20.dp))
            }
        }
        return
    }
    Surface(
        Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
        shape = RoundedCornerShape(14.dp),
        color = if (pending) ex.warning.copy(alpha = 0.10f) else MaterialTheme.colorScheme.surfaceContainer,
        border = androidx.compose.foundation.BorderStroke(1.dp, if (pending) ex.warning.copy(alpha = 0.5f) else ex.cardBorder),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Shield, null, Modifier.size(18.dp), tint = if (pending) ex.warning else ex.muted)
                Spacer(Modifier.width(8.dp))
                Text(verb, style = MaterialTheme.typography.titleSmall)
            }
            // A shell request shows the whole command; a file read/write request highlights basename + directory.
            val cmdStr = permCommand(p, actions)
            val rawTitle = p.title?.trim().orEmpty()
            if (cmdStr == null && rawTitle.startsWith("File ") && rawTitle.contains('/')) {
                val fp = rawTitle.removePrefix("File ").trim()
                val fname = fp.substringAfterLast('/')
                val dir = fp.substringBeforeLast('/', "")
                Surface(
                    Modifier.fillMaxWidth(),
                    shape = RoundedCornerShape(8.dp),
                    color = MaterialTheme.colorScheme.surfaceContainerHighest,
                ) {
                    Column(Modifier.padding(horizontal = 10.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Text(fname, style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold)
                        if (dir.isNotBlank()) {
                            Text(dir, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = ex.muted)
                        }
                    }
                }
            } else {
                CommandBox(cmdStr ?: p.title ?: "tool call")
            }
            // An edit shows what it would change, not just the file name.
            p.callId?.let(actions.toolFor)?.let { t ->
                val patch = t.patchLines()
                when {
                    patch != null -> DiffView(patch, Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)), wrap = true, maxLines = 60)
                    else -> t.diffs().forEach { d ->
                        DiffView(d.lines(), Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)), wrap = true, maxLines = 60)
                    }
                }
            }
            p.reason?.takeIf { pending }?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = ex.muted) }
            p.blockedPath?.takeIf { pending }?.let { Text("Outside the workspace: $it", style = MaterialTheme.typography.bodySmall, color = ex.warning) }
            if (pending) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    p.options.forEach { o ->
                        val allow = o.kind.startsWith("allow")
                        val onClick: () -> Unit = {
                            busy = true
                            scope.launch {
                                runCatching { actions.conn.rpc.call("session.permission", params { put("id", actions.sessionId); put("reqId", p.reqId); put("optionId", o.optionId) }) }
                                busy = false
                            }
                        }
                        val label = permOptionLabel(o, actions.isClaude, actions.isCodex)
                        when {
                            o.kind == "allow_always" -> androidx.compose.material3.FilledTonalButton(onClick = onClick, enabled = !busy) { Text(label) }
                            allow -> Button(onClick = onClick, enabled = !busy) { Text(label) }
                            else -> OutlinedButton(onClick = onClick, enabled = !busy) { Text(label) }
                        }
                    }
                    val allowOnceOpt = p.options.firstOrNull { it.kind.startsWith("allow") }
                    val isReadLikeTool = p.toolKind == "read" || p.toolKind == "search" ||
                        p.tool?.lowercase() in setOf("read_file", "readfile", "view_file", "grep_search", "find_by_name", "list_dir")
                    val isAgyRead = !actions.isClaude && !actions.isCodex &&
                        isReadLikeTool &&
                        p.options.none { it.kind == "allow_always" } &&
                        allowOnceOpt != null
                    if (isAgyRead) {
                        val appCtx = LocalContext.current
                        androidx.compose.material3.FilledTonalButton(
                            enabled = !busy,
                            onClick = {
                                busy = true
                                scope.launch {
                                    appCtx.graph.prefs.update { st -> st.copy(agyFlags = st.agyFlags.copy(autoAllowReads = true)) }
                                    runCatching {
                                        actions.conn.rpc.call("session.permission", params {
                                            put("id", actions.sessionId)
                                            put("reqId", p.reqId)
                                            put("optionId", allowOnceOpt!!.optionId)
                                            put("autoAllowReads", true)
                                        })
                                    }
                                    busy = false
                                }
                            },
                        ) { Text("Always allow reads") }
                    }
                    p.options.firstOrNull { it.kind == "reject_once" }?.let {
                        androidx.compose.material3.TextButton(onClick = { denyNote = "" }, enabled = !busy) { Text("Deny with comment") }
                    }
                }
            }
        }
    }
    denyNote?.let { note ->
        androidx.compose.material3.AlertDialog(
            onDismissRequest = { denyNote = null },
            title = { Text("Deny with comment") },
            text = {
                androidx.compose.material3.OutlinedTextField(note, { denyNote = it }, Modifier.fillMaxWidth(),
                    placeholder = { Text("Tell ${actions.label} what to do instead") }, minLines = 2)
            },
            confirmButton = {
                androidx.compose.material3.TextButton(enabled = note.isNotBlank(), onClick = {
                    val reject = p.options.first { it.kind == "reject_once" }
                    denyNote = null
                    busy = true
                    scope.launch {
                        if (actions.isClaude) {
                            // Claude takes the comment with the denial and carries on with it.
                            runCatching {
                                actions.conn.rpc.call("session.permission", params {
                                    put("id", actions.sessionId); put("reqId", p.reqId); put("optionId", reject.optionId); put("message", note.trim())
                                })
                            }
                        } else {
                            runCatching {
                                actions.conn.rpc.call("session.permission", params { put("id", actions.sessionId); put("reqId", p.reqId); put("optionId", reject.optionId) })
                            }
                            actions.onSendText(note.trim())
                        }
                        busy = false
                    }
                }) { Text("Deny and send") }
            },
            dismissButton = { androidx.compose.material3.TextButton(onClick = { denyNote = null }) { Text("Cancel") } },
        )
    }
}

data class Question(val text: String, val options: List<String>, val multi: Boolean)

fun Tool.questions(): List<Question> = input.obj()?.get("questions").arr()?.mapNotNull { q ->
    val o = q.obj() ?: return@mapNotNull null
    val opts = o["options"].arr()?.mapNotNull { el ->
        (el as? JsonPrimitive)?.contentOrNull
            ?: el.obj()?.let { obj -> obj.str("label") ?: obj.str("name") ?: obj.str("value") }
    } ?: emptyList()
    val multi = (o["is_multi_select"] as? JsonPrimitive)?.booleanOrNull == true ||
        (o["multiSelect"] as? JsonPrimitive)?.booleanOrNull == true
    Question(
        o.str("question") ?: return@mapNotNull null,
        opts,
        multi,
    )
} ?: emptyList()

@Composable
fun QuestionCard(item: Item, active: Boolean, actions: TranscriptActions) {
    val t = item.tool ?: return
    val ex = LocalExtra.current
    val qs = remember(t.input) { t.questions() }
    if (qs.isEmpty()) {
        ToolRow(item, actions, false, "", indent = false); return
    }
    var idx by remember { mutableStateOf(0) }
    val answers = remember { mutableStateMapOf<Int, List<String>>() }
    val other = remember { mutableStateMapOf<Int, String>() }
    Surface(
        Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
        shape = RoundedCornerShape(14.dp),
        color = if (active) MaterialTheme.colorScheme.secondaryContainer.copy(alpha = 0.35f) else MaterialTheme.colorScheme.surfaceContainer,
        border = androidx.compose.foundation.BorderStroke(1.dp, if (active) MaterialTheme.colorScheme.secondary.copy(alpha = 0.5f) else ex.cardBorder),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.HelpOutline, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.secondary)
                Spacer(Modifier.width(8.dp))
                Text(if (active) "${actions.label} has ${if (qs.size == 1) "a question" else "questions"} for you" else "Questions", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                if (qs.size > 1) Text("${idx + 1} of ${qs.size}", style = MaterialTheme.typography.labelSmall, color = ex.muted)
            }
            if (!active) {
                qs.forEach { q -> Text("• ${q.text}", style = MaterialTheme.typography.bodySmall, color = ex.muted) }
                return@Column
            }
            val q = qs[idx]
            Text(q.text, style = MaterialTheme.typography.bodyLarge)
            if (q.multi) Text("Select all that apply", style = MaterialTheme.typography.labelSmall, color = ex.muted)
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                q.options.forEach { raw ->
                    val rec = raw.startsWith("(Recommended)")
                    val label = raw.removePrefix("(Recommended)").trim()
                    val sel = answers[idx]?.contains(label) == true
                    FilterChip(selected = sel, onClick = {
                        val cur = answers[idx].orEmpty()
                        answers[idx] = if (q.multi) (if (sel) cur - label else cur + label) else listOf(label)
                        if (!q.multi) other.remove(idx)
                    }, label = { Text(label + if (rec) "  ★" else "") })
                }
            }
            OutlinedTextField(other[idx] ?: "", { v -> other[idx] = v; if (!q.multi && v.isNotBlank()) answers.remove(idx) },
                placeholder = { Text("Something else…") }, singleLine = false, modifier = Modifier.fillMaxWidth())
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                if (idx > 0) TextButton(onClick = { idx-- }) { Text("Previous") }
                Spacer(Modifier.weight(1f))
                TextButton(onClick = {
                    if (idx < qs.size - 1) {
                        idx++
                    } else {
                        val text = qs.mapIndexed { i, qq ->
                            val parts = answers[i].orEmpty() + listOfNotNull(other[i]?.takeIf { it.isNotBlank() })
                            "${qq.text}: ${if (parts.isEmpty()) "(skipped)" else parts.joinToString(", ")}"
                        }.joinToString("\n")
                        actions.onSendText(text)
                    }
                }) { Text("Skip") }
                val answered = !answers[idx].isNullOrEmpty() || !other[idx].isNullOrBlank()
                if (idx < qs.size - 1) {
                    Button(onClick = { idx++ }, enabled = answered) { Text("Next") }
                } else {
                    Button(onClick = {
                        val text = qs.mapIndexed { i, qq ->
                            val parts = answers[i].orEmpty() + listOfNotNull(other[i]?.takeIf { it.isNotBlank() })
                            "${qq.text}: ${if (parts.isEmpty()) "(skipped)" else parts.joinToString(", ")}"
                        }.joinToString("\n")
                        actions.onSendText(text)
                    }) { Text("Submit") }
                }
            }
        }
    }
}

@Composable
fun PlanReviewCard(item: Item, active: Boolean, actions: TranscriptActions) {
    val t = item.tool ?: return
    val ex = LocalExtra.current
    val meta = t.input.obj()?.get("ArtifactMetadata").obj()
    val content = t.input.str("Content").orEmpty()
    val path = t.path()
    var expanded by remember { mutableStateOf(active) }
    Surface(
        Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
        shape = RoundedCornerShape(14.dp),
        color = MaterialTheme.colorScheme.surfaceContainer,
        border = androidx.compose.foundation.BorderStroke(1.dp, if (active) MaterialTheme.colorScheme.primary.copy(alpha = 0.6f) else ex.cardBorder),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.clickable { expanded = !expanded }) {
                Icon(Icons.Outlined.TaskAlt, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    Text(if (active) "Plan ready for review" else "Plan", style = MaterialTheme.typography.titleSmall)
                    meta.str("Summary")?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = ex.muted) }
                }
                Icon(if (expanded) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore, null)
            }
            if (expanded && content.isNotBlank()) {
                Box(Modifier.fillMaxWidth().heightIn(max = 420.dp).clip(RoundedCornerShape(8.dp)).border(1.dp, ex.cardBorder, RoundedCornerShape(8.dp)).verticalScroll(rememberScrollState()).padding(10.dp)) {
                    MarkdownText(content, immediate = true)
                }
            }
            if (path != null) {
                TextButton(onClick = { actions.onOpenFile(path) }) { Text("Open ${baseName(path)}") }
            }
            if (active) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(onClick = { actions.onApprovePlan(false) }) { Text("Approve plan") }
                    OutlinedButton(onClick = { actions.onApprovePlan(true) }) { Text("Approve & accept edits") }
                    TextButton(onClick = { actions.onPrefill("Please revise the plan: ") }) { Text("Suggest edits") }
                }
            }
        }
    }
}

@Composable
fun TodoCard(item: Item) {
    val ex = LocalExtra.current
    val entries = item.plan.arr() ?: return
    val done = entries.count { it.str("status") == "completed" }
    Surface(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp), shape = RoundedCornerShape(14.dp), color = MaterialTheme.colorScheme.surfaceContainer) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("Plan · $done/${entries.size} done", style = MaterialTheme.typography.titleSmall)
            entries.forEach { e ->
                val st = e.str("status")
                Row(verticalAlignment = Alignment.CenterVertically) {
                    when (st) {
                        "completed" -> Icon(Icons.Outlined.CheckCircle, null, Modifier.size(16.dp), tint = ex.success)
                        "in_progress" -> CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
                        else -> Icon(Icons.Outlined.RadioButtonUnchecked, null, Modifier.size(16.dp), tint = ex.muted)
                    }
                    Spacer(Modifier.width(8.dp))
                    Text(e.str("content") ?: "", style = MaterialTheme.typography.bodySmall, color = if (st == "completed") ex.muted else MaterialTheme.colorScheme.onSurface)
                }
            }
        }
    }
}

@Composable
fun NoticeRow(item: Item) {
    val ex = LocalExtra.current
    val text = item.text.orEmpty().trim()
    if (text.isEmpty()) return
    val uriHandler = LocalUriHandler.current
    if (item.level == "error" || item.level == "warn") {
        val accent = if (item.level == "error") ex.danger else ex.warning
        val annotated = remember(text, accent) { linkifyBubbleText(text, accent) { uriHandler.openUri(it) } }
        Surface(
            color = accent.copy(alpha = 0.10f),
            shape = RoundedCornerShape(10.dp),
            border = androidx.compose.foundation.BorderStroke(1.dp, accent.copy(alpha = 0.35f)),
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
        ) {
            Text(annotated, style = MaterialTheme.typography.bodySmall, color = accent, modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp))
        }
    } else {
        Text(
            text, style = MaterialTheme.typography.bodySmall, color = ex.muted,
            modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 6.dp),
            textAlign = androidx.compose.ui.text.style.TextAlign.Center,
        )
    }
}

@Composable
fun TurnRow(item: Item, actions: TranscriptActions? = null) {
    val ex = LocalExtra.current
    val t = item.turn ?: return
    val label = when (t.stopReason) {
        "end_turn" -> "Done"
        "cancelled" -> "Interrupted"
        "max_tokens" -> "Stopped: output limit"
        "max_turn_requests" -> "Stopped: turn limit"
        "refusal" -> "Refused"
        else -> t.stopReason.ifBlank { "Done" }
    }
    val parts = listOfNotNull(label, fmtDuration(t.durationMs).takeIf { t.durationMs > 0 }, t.usage?.totalTokens?.takeIf { it > 0 }?.let { "${fmtTokens(it)} tokens" },
        t.usage?.costUsd?.takeIf { it > 0 }?.let { fmtUsd(it) })
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 2.dp)) {
        val cp = t.checkpoint
        if (cp != null && (cp.files.isNotEmpty() || cp.totalAdd > 0 || cp.totalDel > 0)) {
            Surface(
                color = MaterialTheme.colorScheme.surfaceContainer,
                shape = RoundedCornerShape(10.dp),
                modifier = Modifier.fillMaxWidth().padding(bottom = 4.dp),
            ) {
                Row(
                    Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween,
                ) {
                    Text(
                        "${cp.files.size} file${if (cp.files.size == 1) "" else "s"}  +${cp.totalAdd} -${cp.totalDel}",
                        style = MaterialTheme.typography.labelMedium,
                        fontFamily = FontFamily.Monospace,
                    )
                    Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                        actions?.onCheckpointDiff?.let { cb ->
                            androidx.compose.material3.TextButton(onClick = { cb(cp.turnIndex) }) { Text("Diff") }
                        }
                        actions?.onCheckpointRevert?.let { cb ->
                            androidx.compose.material3.TextButton(onClick = { cb(cp.turnIndex) }) {
                                Text("Revert", color = ex.danger)
                            }
                        }
                    }
                }
            }
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
            Text(parts.joinToString(" · "), style = MaterialTheme.typography.labelSmall, color = if (t.stopReason == "cancelled") ex.warning else ex.muted)
        }
    }
}

@Composable
fun WorkingIndicator(since: Long, status: String? = null) {
    val ex = LocalExtra.current
    val tr = rememberInfiniteTransition(label = "dots")
    val a by tr.animateFloat(0.3f, 1f, infiniteRepeatable(tween(700), RepeatMode.Reverse), label = "a")
    var now by remember { mutableStateOf(System.currentTimeMillis()) }
    androidx.compose.runtime.LaunchedEffect(Unit) {
        while (true) { kotlinx.coroutines.delay(1000); now = System.currentTimeMillis() }
    }
    Row(Modifier.padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(8.dp).alpha(a).clip(RoundedCornerShape(50)).background(ex.running))
        Spacer(Modifier.width(8.dp))
        val what = when (status) {
            "compacting" -> "Compacting conversation"
            null, "" -> "Working"
            else -> status.replaceFirstChar { it.uppercase() }
        }
        Text(what + if (since > 0) " · ${fmtDuration(now - since)}" else "…", style = MaterialTheme.typography.bodySmall, color = ex.muted)
    }
}

@Suppress("unused")
private fun keep(x: JsonElement) = AppJson.toString()


/** Recognizes a comment message built by the daemon (core/comments.go); returns (file, comment) pairs. */
fun parseFeedback(text: String): List<Pair<String, String>>? {
    if (!text.startsWith("The user has left some comments")) return null
    val out = mutableListOf<Pair<String, String>>()
    var file = ""
    text.lineSequence().forEach { l ->
        when {
            l.startsWith("Comments on artifact URI: ") -> file = l.removePrefix("Comments on artifact URI: ").substringAfterLast('/')
            l.startsWith("Comments on file: ") -> file = l.removePrefix("Comments on file: ").substringAfterLast('/')
            l.startsWith("The user's comment was: ") -> out += file to l.removePrefix("The user's comment was: ")
            l.startsWith("The user approved this.") -> {}
        }
    }
    return out
}
