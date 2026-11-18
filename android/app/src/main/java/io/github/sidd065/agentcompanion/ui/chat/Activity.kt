package io.github.sidd065.agentcompanion.ui.chat

import io.github.sidd065.agentcompanion.ui.components.AppSheet
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.Article
import androidx.compose.material.icons.outlined.Email
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.InsertDriveFile
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.RateReview
import androidx.compose.material.icons.outlined.SmartToy
import androidx.compose.material.icons.outlined.StopCircle
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.Webhook
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.LiveArtifact
import io.github.sidd065.agentcompanion.core.LiveState
import io.github.sidd065.agentcompanion.core.LiveSubagent
import io.github.sidd065.agentcompanion.core.LiveTask
import io.github.sidd065.agentcompanion.core.SubStep
import io.github.sidd065.agentcompanion.core.cleanTerminalText
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.components.MarkdownText
import androidx.compose.material.icons.outlined.Person
import androidx.compose.material.icons.outlined.Info
import io.github.sidd065.agentcompanion.ui.components.sheetScrollGuard
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import kotlinx.serialization.json.put

/** Chips above the composer: running shells, subagents, artifacts awaiting review… */
@Composable
fun ActivityStrip(live: LiveState?, onOpen: (String) -> Unit, onReview: (LiveArtifact) -> Unit) {
    val l = live ?: return
    if (l.tasks.isEmpty() && l.subagents.isEmpty() && l.reviewable.isEmpty() && l.hook.isNullOrBlank() && l.messages.isEmpty() && l.artifacts.isEmpty()) return
    val ex = LocalExtra.current
    Row(
        Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 10.dp, vertical = 2.dp),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        l.reviewable.firstOrNull()?.let { a ->
            AssistChip(
                onClick = { onReview(a) },
                label = { Text("Review ${a.name.substringAfterLast('/').removeSuffix(".md")}", maxLines = 1) },
                leadingIcon = { Icon(Icons.Outlined.RateReview, null, Modifier.size(16.dp), tint = ex.warning) },
                colors = AssistChipDefaults.assistChipColors(containerColor = ex.warning.copy(alpha = 0.12f)),
            )
        }
        if (l.tasks.isNotEmpty()) AssistChip(
            onClick = { onOpen("tasks") },
            label = { Text(if (l.tasks.size == 1) "1 shell running" else "${l.tasks.size} shells running") },
            leadingIcon = { Icon(Icons.Outlined.Terminal, null, Modifier.size(16.dp), tint = ex.running) },
        )
        if (l.subagents.isNotEmpty()) {
            // "Working" only while agy runs and the subagent wrote to its transcript recently.
            val n = if (l.running) l.activeAgents.count { System.currentTimeMillis() - it.updated < 120_000 } else 0
            AssistChip(
                onClick = { onOpen("agents") },
                label = { Text(if (n > 0) "$n agent${if (n > 1) "s" else ""} working" else "${l.subagents.size} subagent${if (l.subagents.size > 1) "s" else ""}") },
                leadingIcon = { Icon(Icons.Outlined.SmartToy, null, Modifier.size(16.dp), tint = if (n > 0) ex.running else ex.muted) },
            )
        }
        if (!l.hook.isNullOrBlank()) AssistChip(
            onClick = {}, label = { Text("Hook: ${l.hook}") },
            leadingIcon = { Icon(Icons.Outlined.Webhook, null, Modifier.size(16.dp)) },
        )
        if (l.artifacts.isNotEmpty()) AssistChip(
            onClick = { onOpen("artifacts") }, label = { Text("${l.artifacts.size} artifact${if (l.artifacts.size > 1) "s" else ""}") },
            leadingIcon = { Icon(Icons.Outlined.Article, null, Modifier.size(16.dp)) },
        )
        if (l.messages.isNotEmpty()) AssistChip(
            onClick = { onOpen("messages") }, label = { Text("${l.messages.size} message${if (l.messages.size > 1) "s" else ""}") },
            leadingIcon = { Icon(Icons.Outlined.Email, null, Modifier.size(16.dp)) },
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ActivitySheet(
    conn: HostConnection,
    sessionId: String,
    live: LiveState,
    initialTab: String,
    onDismiss: () -> Unit,
    initialTaskId: String? = null,
    onOpenArtifact: (LiveArtifact) -> Unit,
) {
    val state = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val tabs = listOf("tasks" to "Shells", "agents" to "Agents", "artifacts" to "Artifacts", "messages" to "Messages")
    var tab by remember { mutableStateOf(tabs.indexOfFirst { it.first == initialTab }.coerceAtLeast(0)) }
    var task by remember { mutableStateOf(live.tasks.firstOrNull { it.id == initialTaskId }) }
    var agent by remember { mutableStateOf<LiveSubagent?>(null) }
    AppSheet(onDismissRequest = onDismiss, sheetState = state) {
        Column(Modifier.fillMaxWidth().fillMaxHeight(0.92f)) {
            when {
                task != null -> TaskOutputView(conn, sessionId, task!!, stillRunning = live.tasks.any { it.id == task!!.id }) { task = null }
                agent != null -> SubagentView(conn, sessionId, live.subagents.firstOrNull { it.id == agent!!.id } ?: agent!!) { agent = null }
                else -> {
                    PrimaryTabRow(selectedTabIndex = tab) {
                        tabs.forEachIndexed { i, (k, l) ->
                            val n = when (k) {
                                "tasks" -> live.tasks.size
                                "agents" -> live.subagents.size
                                "artifacts" -> live.artifacts.size
                                else -> live.messages.size
                            }
                            // The count goes on its own line: "Messages (14)" doesn't fit a quarter of a phone.
                            Tab(selected = tab == i, onClick = { tab = i }, text = {
                                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                                    Text(l, maxLines = 1, softWrap = false)
                                    if (n > 0) Text("$n", style = MaterialTheme.typography.labelSmall)
                                }
                            })
                        }
                    }
                    when (tabs[tab].first) {
                        "tasks" -> ListOrEmpty(live.tasks.isEmpty(), "No shell commands are running in the background.") {
                            items(live.tasks, key = { it.id }) { t ->
                                ActivityRow(Icons.Outlined.Terminal, t.title, t.description.takeIf { it != t.title }, "running · started ${relTime(t.start)}") { task = t }
                            }
                        }
                        "agents" -> ListOrEmpty(live.subagents.isEmpty(), "No subagents in this conversation.") {
                            items(live.subagents, key = { it.id }) { a ->
                                ActivityRow(Icons.Outlined.SmartToy, a.role.ifBlank { "Subagent" }, a.type?.let { "type: $it" },
                                    (if (a.alive) "working" else a.state.lowercase()) + (if (a.updated > 0) " · ${relTime(a.updated)}" else "")) { agent = a }
                            }
                        }
                        "artifacts" -> ListOrEmpty(live.artifacts.isEmpty(), "No artifacts yet.") {
                            items(live.artifacts, key = { it.path.ifBlank { it.uri } }) { a ->
                                ActivityRow(
                                    when (a.kind) { "image", "video" -> Icons.Outlined.Image; "url" -> Icons.Outlined.Link; "markdown" -> Icons.Outlined.Article; else -> Icons.Outlined.InsertDriveFile },
                                    a.name, a.summary,
                                    listOfNotNull(
                                        when {
                                            a.needsReview -> "review requested"
                                            a.requestFeedback && a.answered && a.review == null -> "answered"
                                            else -> a.review?.lowercase()
                                        },
                                        if (a.updated > 0) relTime(a.updated) else null,
                                    ).joinToString(" · "),
                                    highlight = a.needsReview,
                                ) { onOpenArtifact(a) }
                            }
                        }
                        else -> ListOrEmpty(live.messages.isEmpty(), "Messages from subagents and finished background tasks show up here.") {
                            items(live.messages.asReversed(), key = { it.id }) { m -> MessageRow(m) }
                        }
                    }
                }
            }
        }
    }
}

/** A message to the agent: a task's result (console output), your queued message, a notice or a subagent's report. */
@Composable
private fun MessageRow(m: io.github.sidd065.agentcompanion.core.LiveMessage) {
    val ex = LocalExtra.current
    var open by remember { mutableStateOf(false) }
    val kind = m.effectiveKind
    val (icon, fallback) = when (kind) {
        "task" -> Icons.Outlined.Terminal to "Background task"
        "user" -> Icons.Outlined.Person to "Your message"
        "notice" -> Icons.Outlined.Info to "Notice"
        else -> Icons.Outlined.Email to "Message"
    }
    Column(Modifier.fillMaxWidth().clickable { open = !open }.padding(horizontal = 16.dp, vertical = 10.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(icon, null, Modifier.size(18.dp), tint = if (kind == "notice") ex.muted else MaterialTheme.colorScheme.primary)
            Spacer(Modifier.width(10.dp))
            Text(m.title.ifBlank { fallback }, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(relTime(m.ts), style = MaterialTheme.typography.labelSmall, color = ex.muted)
        }
        Spacer(Modifier.size(4.dp))
        when {
            kind == "task" -> {
                // Console output: monospace, scrolls sideways instead of wrapping when open.
                if (open) {
                    SelectionContainer {
                        Text(m.content, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace,
                            modifier = Modifier.fillMaxWidth().background(ex.codeBg, RoundedCornerShape(8.dp)).horizontalScroll(rememberScrollState()).padding(8.dp))
                    }
                } else {
                    Text(m.content, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, maxLines = 4, overflow = TextOverflow.Ellipsis)
                }
            }
            open -> MarkdownText(m.content)
            else -> Text(m.content, style = MaterialTheme.typography.bodySmall, maxLines = 4, overflow = TextOverflow.Ellipsis,
                color = if (kind == "notice") ex.muted else androidx.compose.ui.graphics.Color.Unspecified)
        }
    }
    HorizontalDivider()
}

@Composable
private fun ListOrEmpty(empty: Boolean, emptyText: String, content: androidx.compose.foundation.lazy.LazyListScope.() -> Unit) {
    if (empty) {
        Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) {
            Text(emptyText, style = MaterialTheme.typography.bodyMedium, color = LocalExtra.current.muted)
        }
    } else {
        LazyColumn(Modifier.fillMaxWidth().sheetScrollGuard(), contentPadding = PaddingValues(bottom = 24.dp), content = content)
    }
}

@Composable
private fun ActivityRow(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    title: String,
    subtitle: String?,
    meta: String,
    highlight: Boolean = false,
    onClick: () -> Unit,
) {
    val ex = LocalExtra.current
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick)
            .background(if (highlight) ex.warning.copy(alpha = 0.08f) else androidx.compose.ui.graphics.Color.Transparent)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, Modifier.size(22.dp), tint = if (highlight) ex.warning else MaterialTheme.colorScheme.primary)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
            subtitle?.takeIf { it.isNotBlank() }?.let {
                Text(it, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            Text(meta, style = MaterialTheme.typography.labelSmall, color = if (highlight) ex.warning else ex.muted)
        }
    }
    HorizontalDivider()
}

/** Live output of a background shell command, with Stop. */
@Composable
fun TaskOutputView(conn: HostConnection, sessionId: String, task: LiveTask, stillRunning: Boolean, onBack: () -> Unit) {
    val ex = LocalExtra.current
    val scope = rememberCoroutineScope()
    var text by remember { mutableStateOf("") }
    var offset by remember { mutableLongStateOf(0L) }
    var running by remember { mutableStateOf(true) }
    var stopping by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scroll = rememberScrollState()
    LaunchedEffect(task.id) {
        while (isActive) {
            runCatching {
                conn.rpc.call("session.taskOutput", params { put("id", sessionId); put("taskId", task.id); put("offset", offset) }).jsonObject
            }.onSuccess { r ->
                val data = r["data"]?.jsonPrimitive?.content.orEmpty()
                val from = r["offset"]?.jsonPrimitive?.longOrNull ?: 0
                text = if (offset == 0L) data else (text + data).takeLast(400_000)
                offset = r["next"]?.jsonPrimitive?.longOrNull ?: (from + data.length)
                running = r["running"]?.jsonPrimitive?.content == "true"
                error = null
            }.onFailure { error = it.message }
            delay(if (running) 1000 else 4000)
        }
    }
    LaunchedEffect(text.length) { scroll.animateScrollTo(scroll.maxValue) }
    Column(Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
        Row(Modifier.padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") }
            Column(Modifier.weight(1f)) {
                Text(task.title, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(if (running && stillRunning) "running · started ${relTime(task.start)}" else "finished", style = MaterialTheme.typography.labelSmall,
                    color = if (running) ex.running else ex.muted)
            }
            if (running && stillRunning) OutlinedButton(enabled = !stopping, onClick = {
                stopping = true
                scope.launch {
                    runCatching { conn.rpc.call("session.taskStop", params { put("id", sessionId); put("step", task.step); put("taskId", task.id) }) }.onFailure { error = it.message }
                    stopping = false
                }
            }) {
                Icon(Icons.Outlined.StopCircle, null, Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp))
                Text("Stop")
            }
            Spacer(Modifier.width(8.dp))
        }
        Text("$ " + task.description, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, color = ex.muted,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp), maxLines = 4, overflow = TextOverflow.Ellipsis)
        error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(horizontal = 16.dp)) }
        Surface(color = ex.codeBg, shape = RoundedCornerShape(10.dp), modifier = Modifier.fillMaxWidth().weight(1f, fill = false).padding(horizontal = 12.dp)) {
            SelectionContainer {
                Text(
                    cleanTerminalText(text).ifEmpty { "(no output yet)" },
                    style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace,
                    modifier = Modifier.fillMaxWidth().heightIn(min = 120.dp).sheetScrollGuard().verticalScroll(scroll).padding(10.dp),
                )
            }
        }
    }
}

/** A subagent's transcript, refreshed while it works, with Stop. */
@Composable
fun SubagentView(conn: HostConnection, sessionId: String, agent: LiveSubagent, onBack: () -> Unit) {
    val ex = LocalExtra.current
    val scope = rememberCoroutineScope()
    var steps by remember { mutableStateOf<List<SubStep>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    val list = rememberLazyListState()
    LaunchedEffect(agent.id) {
        while (isActive) {
            runCatching {
                conn.rpc.call("session.subagent", params { put("id", sessionId); put("subId", agent.id) }).jsonObject["steps"]!!
                    .decode(ListSerializer(SubStep.serializer()))
            }.onSuccess { steps = it; error = null }.onFailure { error = it.message }
            delay(if (agent.alive) 2000 else 10_000)
        }
    }
    val visible = steps.orEmpty().filter { it.type != "SYSTEM_MESSAGE" }
    LaunchedEffect(visible.size) { if (visible.isNotEmpty()) list.animateScrollToItem(visible.size - 1) }
    Column(Modifier.fillMaxWidth()) {
        Row(Modifier.padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") }
            Column(Modifier.weight(1f)) {
                Text(agent.role.ifBlank { "Subagent" }, style = MaterialTheme.typography.titleMedium)
                Text(listOfNotNull(agent.type, if (agent.alive) "working" else agent.state.lowercase()).joinToString(" · "),
                    style = MaterialTheme.typography.labelSmall, color = if (agent.alive) ex.running else ex.muted)
            }
            if (agent.alive) OutlinedButton(onClick = {
                scope.launch { runCatching { conn.rpc.call("session.subagentStop", params { put("id", sessionId); put("subId", agent.id) }) }.onFailure { error = it.message } }
            }) {
                Icon(Icons.Outlined.StopCircle, null, Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp))
                Text("Stop")
            }
            Spacer(Modifier.width(8.dp))
        }
        error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(horizontal = 16.dp)) }
        if (steps == null) Text("Loading…", Modifier.padding(16.dp), color = ex.muted)
        LazyColumn(state = list, modifier = Modifier.fillMaxWidth().sheetScrollGuard(), contentPadding = PaddingValues(start = 12.dp, end = 12.dp, bottom = 24.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)) {
            items(visible, key = { it.i }) { st -> SubStepRow(st) }
        }
    }
}

private val tagRe = Regex("</?(USER_REQUEST|ADDITIONAL_METADATA|USER_SETTINGS_CHANGE)>")

@Composable
private fun SubStepRow(st: SubStep) {
    val ex = LocalExtra.current
    // agy names its steps USER_INPUT / PLANNER_RESPONSE; Claude's are user / assistant (tool results: tool).
    when (st.type) {
        "USER_INPUT", "user" -> Surface(color = MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.5f), shape = RoundedCornerShape(12.dp)) {
            Column(Modifier.fillMaxWidth().padding(10.dp)) {
                Text("Task", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                Text(st.text.orEmpty().substringBefore("<ADDITIONAL_METADATA>").replace(tagRe, "").trim(), style = MaterialTheme.typography.bodyMedium)
            }
        }
        "PLANNER_RESPONSE", "assistant" -> Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            st.thinking?.takeIf { it.isNotBlank() }?.let { th ->
                var open by remember { mutableStateOf(false) }
                Text(if (open) th.trim() else "Thinking: " + th.trim().lineSequence().first().removePrefix("**").removeSuffix("**"),
                    style = MaterialTheme.typography.bodySmall, color = ex.muted, maxLines = if (open) Int.MAX_VALUE else 1,
                    overflow = TextOverflow.Ellipsis, modifier = Modifier.clickable { open = !open })
            }
            st.text?.takeIf { it.isNotBlank() }?.let { MarkdownText(it) }
            st.tools.forEach { t ->
                Surface(color = ex.codeBg, shape = RoundedCornerShape(8.dp)) {
                    Column(Modifier.fillMaxWidth().padding(8.dp)) {
                        Text(t.name, style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.SemiBold)
                        val summary = subToolSummary(t.args)
                        if (summary.isNotBlank()) Text(summary, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, maxLines = 6, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
        else -> st.text?.takeIf { it.isNotBlank() }?.let { out ->
            var open by remember { mutableStateOf(false) }
            Surface(color = ex.codeBg.copy(alpha = 0.6f), shape = RoundedCornerShape(8.dp), modifier = Modifier.clickable { open = !open }) {
                Column(Modifier.fillMaxWidth().padding(8.dp)) {
                    Text(if (open) "Output" else "Output · tap to expand", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                    Text(cleanTerminalText(out).trim(), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace,
                        maxLines = if (open) Int.MAX_VALUE else 4, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

private val argField = Regex(""""(command|file_path|path|pattern|url|query|description|prompt|skill)"\s*:\s*"((?:[^"\\]|\\.)*)""")

/** A tool call's input in a line: the command, file or pattern, else the raw arguments. */
internal fun subToolSummary(args: String): String {
    if (args.isBlank()) return ""
    val fields = argField.findAll(args).associate { it.groupValues[1] to it.groupValues[2] }
    val pick = listOf("command", "file_path", "path", "pattern", "url", "query", "skill", "description", "prompt").firstNotNullOfOrNull { fields[it] }
    return (pick ?: return args).replace("\\n", "\n").replace("\\\"", "\"").replace("\\\\", "\\")
}
