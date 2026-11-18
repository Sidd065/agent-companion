package io.github.sidd065.agentcompanion.ui.sessions

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.WrapText
import androidx.compose.material.icons.outlined.ExpandLess
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import kotlinx.coroutines.launch
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.AddComment
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.AgentFileDiff
import io.github.sidd065.agentcompanion.core.GitDiff
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.components.DiffLine
import io.github.sidd065.agentcompanion.ui.components.DiffView
import io.github.sidd065.agentcompanion.ui.components.EmptyState
import io.github.sidd065.agentcompanion.ui.components.ErrorBox
import io.github.sidd065.agentcompanion.ui.components.LineDiff
import io.github.sidd065.agentcompanion.ui.components.LoadingBox
import io.github.sidd065.agentcompanion.ui.components.UnifiedDiff
import io.github.sidd065.agentcompanion.ui.components.baseName
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.put

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DiffScreen(sessionId: String?, path: String) {
    val nav = LocalNav.current
    val conn = activeConnection() ?: return
    val who = sessionId?.let { conn.session(it)?.agentLabel } ?: "the agent"
    val g = LocalContext.current.graph
    val settings by g.prefs.flow.collectAsState()
    var tab by remember { mutableIntStateOf(0) }
    var reload by remember { mutableIntStateOf(0) }
    var git by remember { mutableStateOf<GitDiff?>(null) }
    var agent by remember { mutableStateOf<List<AgentFileDiff>?>(null) }
    var err by remember { mutableStateOf<String?>(null) }
    // Line comments (sent to the agent as diff feedback).
    var commenting by remember { mutableStateOf(false) }
    val drafts = remember { androidx.compose.runtime.mutableStateListOf<io.github.sidd065.agentcompanion.core.DraftComment>() }
    var editing by remember { mutableStateOf<Pair<String, DiffLine>?>(null) }
    val snack = io.github.sidd065.agentcompanion.ui.components.LocalSnack.current
    val scope = androidx.compose.runtime.rememberCoroutineScope()
    fun lineNo(l: DiffLine) = if (l.type == '-') l.oldNo else l.newNo
    fun draftFor(path: String, l: DiffLine) = drafts.firstOrNull { it.uri == path && it.startLine == lineNo(l) && it.quote == l.text }
    val onLine: ((String, DiffLine) -> Unit)? = if (commenting && sessionId != null) { p, l -> editing = p to l } else null
    LaunchedEffect(reload) {
        err = null; git = null
        runCatching {
            git = if (sessionId != null) conn.rpc.call("session.diff", params { put("id", sessionId) }).decode(GitDiff.serializer())
            else conn.rpc.call("git.diff", params { put("path", path) }).decode(GitDiff.serializer())
        }.onFailure { err = it.message }
        if (sessionId != null) {
            runCatching { agent = conn.rpc.call("session.changes", params { put("id", sessionId) }).decode(ListSerializer(AgentFileDiff.serializer())) }
                .onFailure { agent = emptyList() }
        }
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Changes") },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) } },
                actions = {
                    if (sessionId != null) IconButton(onClick = { commenting = !commenting }) {
                        Icon(if (commenting) Icons.Outlined.Close else Icons.Outlined.AddComment, if (commenting) "Stop commenting" else "Comment on lines")
                    }
                    IconButton(onClick = { scope.launch { g.prefs.update { it.copy(codeWrap = !it.codeWrap) } } }) {
                        Icon(Icons.AutoMirrored.Outlined.WrapText, if (settings.codeWrap) "Don't wrap lines" else "Wrap lines",
                            tint = if (settings.codeWrap) MaterialTheme.colorScheme.primary else androidx.compose.ui.graphics.Color.Unspecified)
                    }
                    IconButton(onClick = { reload++ }) { Icon(Icons.Outlined.Refresh, "Refresh") }
                },
            )
        },
        bottomBar = {
            if (commenting || drafts.isNotEmpty()) androidx.compose.material3.Surface(tonalElevation = 3.dp, modifier = Modifier.navigationBarsPadding()) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(if (drafts.isEmpty()) "Tap a line to comment on it" else "${drafts.size} comment${if (drafts.size > 1) "s" else ""}",
                        style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f))
                    if (commenting) androidx.compose.material3.TextButton(onClick = { commenting = false }) { Text("Done") }
                    if (drafts.isNotEmpty() && sessionId != null) androidx.compose.material3.Button(onClick = {
                        val list = drafts.toList()
                        scope.launch {
                            runCatching {
                                conn.rpc.call("session.comment", params {
                                    put("id", sessionId)
                                    put("comments", io.github.sidd065.agentcompanion.core.AppJson.encodeToJsonElement(
                                        ListSerializer(io.github.sidd065.agentcompanion.core.DraftComment.serializer()), list))
                                })
                            }.onSuccess { drafts.clear(); commenting = false; snack.showSnackbar("Comments sent to $who"); nav.popBackStack() }
                                .onFailure { snack.showSnackbar("Couldn't send: ${it.message}") }
                        }
                    }) { Text("Send") }
                }
            }
        },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            val tabs = if (sessionId != null) listOf("Uncommitted", "Agent edits", "Commits") else listOf("Uncommitted", "Commits")
            PrimaryTabRow(selectedTabIndex = tab.coerceAtMost(tabs.size - 1)) {
                tabs.forEachIndexed { i, name ->
                    Tab(tab == i, { tab = i }, text = { Text(if (name == "Agent edits") "Agent edits${agent?.size?.let { " · $it" } ?: ""}" else name) })
                }
            }
            err?.let { ErrorBox(it) { reload++ } }
            val repoPath = git?.root ?: sessionId?.let { conn.session(it)?.cwd } ?: path
            if (tabs.getOrNull(tab) == "Commits") {
                CommitsTab(conn, repoPath, settings.codeWrap, reload) { p -> nav.navigate(Routes.file(p)) }
            } else if (tab == 0) {
                val d = git
                when {
                    d == null && err == null -> LoadingBox(text = "Running git diff…")
                    d == null -> {}
                    !d.repo -> EmptyState(Icons.Outlined.Refresh, "Not a git repository", "Switch to “Agent edits” to see what $who changed.")
                    d.diff.isBlank() -> EmptyState(Icons.Outlined.Refresh, "No uncommitted changes",
                        listOfNotNull(d.branch?.let { "On branch $it." }, "Committed work is under Commits.").joinToString(" "))
                    else -> {
                        val files = remember(d.diff) { UnifiedDiff.parse(d.diff) }
                        FileDiffList(
                            header = "${d.branch ?: ""} · ${filesLabel(files.size)} · +${files.sumOf { it.added }} −${files.sumOf { it.removed }}" + if (d.truncated) " (truncated)" else "",
                            files = files.map { Triple(it.path, it.lines, it.binary) },
                            wrap = settings.codeWrap,
                            onOpen = { p -> nav.navigate(Routes.file((d.root ?: "") + "/" + p)) },
                            onLine = onLine?.let { f -> { p: String, l: DiffLine -> f((d.root ?: "") + "/" + p, l) } },
                            marked = { p, l -> draftFor((d.root ?: "") + "/" + p, l) != null },
                        )
                    }
                }
            } else {
                val a = agent
                when {
                    a == null -> LoadingBox()
                    a.isEmpty() -> EmptyState(Icons.Outlined.Refresh, "No file edits recorded", "$who hasn't changed any files in this conversation.")
                    else -> {
                        val files = remember(a) {
                            a.map { f ->
                                val p = f.uri.removePrefix("file://")
                                Triple(p, LineDiff.diff(f.originalContent, f.modifiedContent), false)
                            }
                        }
                        FileDiffList("${filesLabel(files.size)} edited by $who", files, settings.codeWrap, onOpen = { p -> nav.navigate(Routes.file(p)) },
                            onLine = onLine, marked = { p, l -> draftFor(p, l) != null })
                    }
                }
            }
        }
    }
    editing?.let { (p, l) ->
        val existing = draftFor(p, l)
        io.github.sidd065.agentcompanion.ui.chat.CommentSheet(
            quote = l.text, initial = existing?.comment.orEmpty(), existing = existing != null,
            title = "Comment on line ${lineNo(l)} of ${baseName(p)}",
            onDismiss = { editing = null }, onDelete = { drafts.remove(existing); editing = null },
        ) { text ->
            drafts.remove(existing)
            drafts.add(io.github.sidd065.agentcompanion.core.DraftComment(kind = "diff", uri = p, startLine = lineNo(l), endLine = lineNo(l), quote = l.text, comment = text))
            editing = null
        }
    }
}

/** Recent commits of the repo at [path]; one opens as a diff. */
@Composable
private fun CommitsTab(conn: io.github.sidd065.agentcompanion.net.HostConnection, path: String, wrap: Boolean, reload: Int, onOpen: (String) -> Unit) {
    val ex = LocalExtra.current
    var log by remember { mutableStateOf<io.github.sidd065.agentcompanion.core.GitLog?>(null) }
    var err by remember { mutableStateOf<String?>(null) }
    var open by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf<String?>(null) }
    var shown by remember { mutableStateOf<GitDiff?>(null) }
    LaunchedEffect(path, reload) {
        err = null; log = null
        runCatching { log = conn.rpc.call("git.log", params { put("path", path); put("limit", 40) }).decode(io.github.sidd065.agentcompanion.core.GitLog.serializer()) }
            .onFailure { err = it.message }
    }
    LaunchedEffect(open, reload) {
        shown = null
        val sha = open ?: return@LaunchedEffect
        runCatching { shown = conn.rpc.call("git.show", params { put("path", path); put("sha", sha) }).decode(GitDiff.serializer()) }
            .onFailure { err = it.message }
    }
    androidx.activity.compose.BackHandler(enabled = open != null) { open = null }
    err?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(16.dp)) }
    if (open != null) {
        val d = shown
        if (d == null) { LoadingBox(text = "Reading the commit…"); return }
        val files = remember(d.diff) { UnifiedDiff.parse(d.diff) }
        Column(Modifier.fillMaxSize()) {
            Row(Modifier.fillMaxWidth().clickable { open = null }.padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.AutoMirrored.Filled.ArrowBack, "All commits", Modifier.size(20.dp))
                Spacer(Modifier.width(8.dp))
                Text(d.subject ?: d.sha.orEmpty(), style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            if (files.isEmpty()) EmptyState(Icons.Outlined.Refresh, "No file changes", "This commit changes no files.")
            else FileDiffList(
                header = "${d.sha?.take(7).orEmpty()} · ${filesLabel(files.size)} · +${files.sumOf { it.added }} −${files.sumOf { it.removed }}" + if (d.truncated) " (truncated)" else "",
                files = files.map { Triple(it.path, it.lines, it.binary) }, wrap = wrap,
                onOpen = { p -> onOpen((d.root ?: path) + "/" + p) },
            )
        }
        return
    }
    val l = log
    when {
        l == null && err == null -> LoadingBox(text = "Reading git log…")
        l == null -> {}
        !l.repo -> EmptyState(Icons.Outlined.Refresh, "Not a git repository", null)
        l.commits.isEmpty() -> EmptyState(Icons.Outlined.Refresh, "No commits yet", null)
        else -> {
            val mine = l.commits.filter { it.mine }
            val rest = l.commits.filterNot { it.mine }
            LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 8.dp)) {
                if (mine.isNotEmpty()) {
                    item { Text("On ${l.branch ?: "this branch"} · not on main", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) }
                    items(mine, key = { "m" + it.sha }) { c -> CommitRow(c) { open = c.sha } }
                }
                if (rest.isNotEmpty()) {
                    item { Text(if (mine.isEmpty()) "Recent commits" else "Earlier", style = MaterialTheme.typography.labelLarge, color = ex.muted,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) }
                    items(rest, key = { "r" + it.sha }) { c -> CommitRow(c) { open = c.sha } }
                }
            }
        }
    }
}

@Composable
private fun CommitRow(c: io.github.sidd065.agentcompanion.core.GitCommit, onClick: () -> Unit) {
    val ex = LocalExtra.current
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(c.subject, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Text(listOf(c.short, c.author, io.github.sidd065.agentcompanion.ui.components.relTime(c.time), "${c.files} file${if (c.files == 1) "" else "s"}").joinToString(" · "),
                style = MaterialTheme.typography.labelSmall, color = ex.muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Spacer(Modifier.width(8.dp))
        Text("+${c.added}", color = ex.diffAdd, style = MaterialTheme.typography.labelMedium)
        Spacer(Modifier.width(6.dp))
        Text("−${c.removed}", color = ex.diffDel, style = MaterialTheme.typography.labelMedium)
    }
}

@Composable
private fun FileDiffList(
    header: String,
    files: List<Triple<String, List<DiffLine>, Boolean>>,
    wrap: Boolean,
    onOpen: (String) -> Unit,
    onLine: ((String, DiffLine) -> Unit)? = null,
    marked: (String, DiffLine) -> Boolean = { _, _ -> false },
) {
    val ex = LocalExtra.current
    val open = remember { mutableStateMapOf<String, Boolean>() }
    val list = androidx.compose.foundation.lazy.rememberLazyListState()
    val scope = androidx.compose.runtime.rememberCoroutineScope()
    LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(12.dp)) {
        item { Text(header, style = MaterialTheme.typography.labelLarge, color = ex.muted, modifier = Modifier.padding(4.dp)) }
        itemsIndexed(files, key = { _, f -> f.first }) { index, (path, lines, binary) ->
            val isOpen = open[path] ?: (files.size <= 3)
            val added = lines.count { it.type == '+' }
            val removed = lines.count { it.type == '-' }
            Column(Modifier.fillMaxWidth().padding(vertical = 4.dp).clip(RoundedCornerShape(10.dp))) {
                Row(
                    Modifier.fillMaxWidth().clickable {
                        open[path] = !isOpen
                        // An opened file's diff starts at the top of the screen, not below the fold.
                        if (!isOpen) scope.launch { list.animateScrollToItem(index + 1) }
                    }.padding(horizontal = 6.dp, vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(if (isOpen) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore, null)
                    Spacer(Modifier.width(6.dp))
                    Column(Modifier.weight(1f)) {
                        Text(baseName(path), style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(path, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = ex.muted, maxLines = 1, overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.clickable { onOpen(path) })
                    }
                    Text("+$added", color = ex.diffAdd, style = MaterialTheme.typography.labelMedium)
                    Spacer(Modifier.width(6.dp))
                    Text("−$removed", color = ex.diffDel, style = MaterialTheme.typography.labelMedium)
                }
                if (isOpen) {
                    if (binary) Text("Binary file", style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(12.dp))
                    else DiffView(lines, Modifier.fillMaxWidth(), wrap = wrap, maxLines = 600,
                        onLineClick = onLine?.let { f -> { l: DiffLine -> f(path, l) } }, marked = { l -> marked(path, l) })
                }
            }
        }
    }
}

private fun filesLabel(n: Int) = if (n == 1) "1 file" else "$n files"
