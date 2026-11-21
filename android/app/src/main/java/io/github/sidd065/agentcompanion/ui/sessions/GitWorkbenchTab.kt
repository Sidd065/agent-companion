package io.github.sidd065.agentcompanion.ui.sessions

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.AutoAwesome
import androidx.compose.material.icons.outlined.CallSplit
import androidx.compose.material.icons.outlined.CloudUpload
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Remove
import androidx.compose.material.icons.outlined.Undo
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.GitBranch
import io.github.sidd065.agentcompanion.core.GitStatus
import io.github.sidd065.agentcompanion.core.PullRequestSummary
import io.github.sidd065.agentcompanion.core.WorktreeInfo
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.components.EmptyState
import io.github.sidd065.agentcompanion.ui.components.LoadingBox
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

@Composable
fun GitWorkbenchTab(
    conn: HostConnection,
    repoPath: String,
    reloadKey: Int,
    onOpenNewInWorktree: (String) -> Unit,
) {
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val ex = LocalExtra.current

    var refresh by remember { mutableIntStateOf(0) }
    var status by remember { mutableStateOf<GitStatus?>(null) }
    var branches by remember { mutableStateOf<List<GitBranch>>(emptyList()) }
    var worktrees by remember { mutableStateOf<List<WorktreeInfo>>(emptyList()) }
    var prs by remember { mutableStateOf<List<PullRequestSummary>>(emptyList()) }
    var section by remember { mutableStateOf("changes") } // changes | branches | worktrees | prs
    var commitMsg by remember { mutableStateOf("") }
    var newBranch by remember { mutableStateOf("") }
    var newWtBranch by remember { mutableStateOf("") }
    var confirmDefaultPush by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }

    LaunchedEffect(repoPath, reloadKey, refresh) {
        if (repoPath.isBlank()) return@LaunchedEffect
        runCatching {
            val st = conn.rpc.call("git.status", params { put("path", repoPath); put("force", refresh > 0) })
                .decode(GitStatus.serializer())
            status = st
            if (st.isRepo) {
                branches = runCatching {
                    conn.rpc.call("git.branches", params { put("path", repoPath) })
                        .decode(ListSerializer(GitBranch.serializer()))
                }.getOrDefault(emptyList())
                worktrees = runCatching {
                    conn.rpc.call("git.worktrees.list", params { put("path", repoPath) })
                        .decode(ListSerializer(WorktreeInfo.serializer()))
                }.getOrDefault(emptyList())
                if (st.forge == "github") {
                    prs = runCatching {
                        conn.rpc.call("pr.list", params { put("path", repoPath); put("state", "open"); put("limit", 15) })
                            .decode(ListSerializer(PullRequestSummary.serializer()))
                    }.getOrDefault(emptyList())
                }
            }
        }.onFailure { snack.showSnackbar("Git status: ${it.message}") }
    }

    fun runGit(label: String, method: String, body: kotlinx.serialization.json.JsonObjectBuilder.() -> Unit) {
        scope.launch {
            busy = true
            runCatching {
                conn.rpc.call(method, params { put("path", repoPath); body() })
            }.onSuccess {
                snack.showSnackbar(label)
                refresh++
            }.onFailure {
                snack.showSnackbar("$label failed: ${it.message}")
            }
            busy = false
        }
    }

    val st = status
    if (st == null) {
        LoadingBox(text = "Loading Git status…")
        return
    }
    if (!st.isRepo) {
        EmptyState(Icons.Outlined.CallSplit, "Not a Git repository", "This folder is not tracked by Git.")
        return
    }

    val staged = st.files.filter { !it.staged.isNullOrBlank() }
    val unstaged = st.files.filter { !it.unstaged.isNullOrBlank() || it.untracked }

    LazyColumn(
        modifier = Modifier.fillMaxSize().padding(horizontal = 12.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        item {
            Spacer(Modifier.height(4.dp))
            Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(
                                text = st.branch ?: "HEAD",
                                style = MaterialTheme.typography.titleSmall,
                                fontFamily = FontFamily.Monospace,
                            )
                            Text(
                                text = buildString {
                                    append("${st.files.size} changed · +${st.totalAdd} -${st.totalDel}")
                                    if (st.ahead > 0) append(" · ↑${st.ahead}")
                                    if (st.behind > 0) append(" · ↓${st.behind}")
                                },
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        IconButton(onClick = { refresh++ }) { Icon(Icons.Outlined.Refresh, "Refresh") }
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(enabled = !busy, onClick = { runGit("Fetched", "git.fetch") {} }) { Text("Fetch") }
                        OutlinedButton(enabled = !busy, onClick = { runGit("Pulled", "git.pull") { put("rebase", false) } }) { Text("Pull") }
                        Button(
                            enabled = !busy,
                            onClick = {
                                if (st.isDefaultBranch) confirmDefaultPush = true
                                else runGit("Pushed", "git.push") { put("setUpstream", true); put("allowDefault", false) }
                            },
                        ) {
                            Icon(Icons.Outlined.CloudUpload, null, Modifier.size(16.dp))
                            Spacer(Modifier.width(6.dp))
                            Text("Push")
                        }
                    }
                }
            }
        }

        item {
            Row(
                Modifier.horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                FilterChip(selected = section == "changes", onClick = { section = "changes" }, label = { Text("Changes (${st.files.size})") })
                FilterChip(selected = section == "branches", onClick = { section = "branches" }, label = { Text("Branches (${branches.count { !it.remote }})") })
                FilterChip(selected = section == "worktrees", onClick = { section = "worktrees" }, label = { Text("Worktrees (${worktrees.size})") })
                FilterChip(selected = section == "prs", onClick = { section = "prs" }, label = { Text("PRs (${prs.size})") })
            }
        }

        if (section == "changes") {
            item {
                Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow)) {
                    Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedTextField(
                            value = commitMsg,
                            onValueChange = { commitMsg = it },
                            modifier = Modifier.fillMaxWidth(),
                            placeholder = { Text("Commit message…") },
                            minLines = 2,
                        )
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                            OutlinedButton(
                                enabled = !busy,
                                onClick = {
                                    scope.launch {
                                        busy = true
                                        runCatching {
                                            val r = conn.rpc.call("git.commitMessage", params { put("path", repoPath) }).jsonObject
                                            r["message"]?.jsonPrimitive?.content?.let { commitMsg = it }
                                        }.onFailure { snack.showSnackbar("Suggest failed: ${it.message}") }
                                        busy = false
                                    }
                                },
                            ) {
                                Icon(Icons.Outlined.AutoAwesome, null, Modifier.size(16.dp))
                                Spacer(Modifier.width(6.dp))
                                Text("Suggest")
                            }
                            Button(
                                enabled = !busy && commitMsg.isNotBlank(),
                                onClick = {
                                    val msg = commitMsg.trim()
                                    commitMsg = ""
                                    runGit("Committed", "git.commit") {
                                        put("message", msg)
                                        put("stageAll", staged.isEmpty())
                                    }
                                },
                            ) {
                                Text(if (staged.isEmpty()) "Commit All" else "Commit (${staged.size})")
                            }
                        }
                    }
                }
            }

            if (staged.isNotEmpty()) {
                item {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                        Text("Staged (${staged.size})", style = MaterialTheme.typography.labelLarge)
                        TextButton(onClick = { runGit("Unstaged all", "git.unstage") { put("files", JsonArray(emptyList())) } }) {
                            Text("Unstage all")
                        }
                    }
                }
                items(staged, key = { "s:${it.path}" }) { f ->
                    Row(Modifier.fillMaxWidth().padding(vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(f.staged ?: "M", color = ex.success, fontFamily = FontFamily.Monospace, modifier = Modifier.width(20.dp))
                        Text(f.path, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        IconButton(onClick = { runGit("Unstaged", "git.unstage") { put("files", JsonArray(listOf(JsonPrimitive(f.path)))) } }) {
                            Icon(Icons.Outlined.Remove, "Unstage", Modifier.size(18.dp))
                        }
                    }
                }
            }

            if (unstaged.isNotEmpty()) {
                item {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                        Text("Unstaged (${unstaged.size})", style = MaterialTheme.typography.labelLarge)
                        TextButton(onClick = { runGit("Staged all", "git.stage") { put("files", JsonArray(emptyList())) } }) {
                            Text("Stage all")
                        }
                    }
                }
                items(unstaged, key = { "u:${it.path}" }) { f ->
                    Row(Modifier.fillMaxWidth().padding(vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(if (f.untracked) "?" else (f.unstaged ?: "M"), color = ex.warning, fontFamily = FontFamily.Monospace, modifier = Modifier.width(20.dp))
                        Text(f.path, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        IconButton(onClick = { runGit("Staged", "git.stage") { put("files", JsonArray(listOf(JsonPrimitive(f.path)))) } }) {
                            Icon(Icons.Outlined.Add, "Stage", Modifier.size(18.dp))
                        }
                        IconButton(onClick = { runGit("Discarded", "git.discard") { put("files", JsonArray(listOf(JsonPrimitive(f.path)))) } }) {
                            Icon(Icons.Outlined.Undo, "Discard", Modifier.size(18.dp))
                        }
                    }
                }
            }
        }

        if (section == "branches") {
            item {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(newBranch, { newBranch = it }, modifier = Modifier.weight(1f), placeholder = { Text("New branch name…") }, singleLine = true)
                    Button(
                        enabled = !busy && newBranch.isNotBlank(),
                        onClick = {
                            val b = newBranch.trim()
                            newBranch = ""
                            runGit("Created $b", "git.checkout") { put("branch", b); put("create", true) }
                        },
                    ) { Text("Create") }
                }
            }
            items(branches.filter { !it.remote }, key = { it.name }) { b ->
                Card(colors = CardDefaults.cardColors(containerColor = if (b.current) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surfaceContainerLow)) {
                    Row(Modifier.fillMaxWidth().padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(b.name, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodyMedium)
                            b.subject?.takeIf { it.isNotBlank() }?.let {
                                Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                        if (!b.current) {
                            TextButton(onClick = { runGit("Checked out ${b.name}", "git.checkout") { put("branch", b.name) } }) {
                                Text("Checkout")
                            }
                        }
                    }
                }
            }
        }

        if (section == "worktrees") {
            item {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(newWtBranch, { newWtBranch = it }, modifier = Modifier.weight(1f), placeholder = { Text("Worktree branch (optional)…") }, singleLine = true)
                    Button(
                        enabled = !busy,
                        onClick = {
                            val b = newWtBranch.trim()
                            newWtBranch = ""
                            runGit("Created worktree", "git.worktrees.create") { if (b.isNotBlank()) put("branchName", b) }
                        },
                    ) { Text("Create") }
                }
            }
            items(worktrees, key = { it.path }) { wt ->
                Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow)) {
                    Column(Modifier.fillMaxWidth().padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Text(wt.branch ?: "(detached)", fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                            TextButton(onClick = { onOpenNewInWorktree(wt.path) }) { Text("New chat") }
                            if (!wt.isMain) {
                                IconButton(onClick = {
                                    runGit("Removed worktree", "git.worktrees.remove") {
                                        put("worktreePath", wt.path)
                                        put("force", true)
                                    }
                                }) { Icon(Icons.Outlined.Delete, "Remove", Modifier.size(18.dp)) }
                            }
                        }
                        Text(wt.path, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }

        if (section == "prs") {
            if (prs.isEmpty()) {
                item {
                    Text(
                        "No open GitHub Pull Requests found (requires `gh` CLI authenticated on the host).",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(16.dp),
                    )
                }
            } else {
                items(prs, key = { it.number }) { pr ->
                    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow)) {
                        Column(Modifier.fillMaxWidth().padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text("#${pr.number} ${pr.title}", style = MaterialTheme.typography.titleSmall)
                            Text(
                                "${pr.headRefName} → ${pr.baseRefName} · +${pr.additions} -${pr.deletions} · ${pr.author}",
                                style = MaterialTheme.typography.bodySmall,
                                fontFamily = FontFamily.Monospace,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            HorizontalDivider()
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                OutlinedButton(onClick = {
                                    scope.launch {
                                        runCatching {
                                            val wt = conn.rpc.call("pr.checkoutWorktree", params { put("path", repoPath); put("number", pr.number) })
                                                .decode(WorktreeInfo.serializer())
                                            snack.showSnackbar("Checked out PR #${pr.number}")
                                            onOpenNewInWorktree(wt.path)
                                        }.onFailure { snack.showSnackbar("Checkout PR failed: ${it.message}") }
                                    }
                                }) { Text("Checkout Worktree") }
                            }
                        }
                    }
                }
            }
        }

        item { Spacer(Modifier.height(32.dp)) }
    }

    if (confirmDefaultPush) {
        AlertDialog(
            onDismissRequest = { confirmDefaultPush = false },
            title = { Text("Push to default branch?") },
            text = { Text("You are on ${st.branch}. Confirm if you intend to push directly to the default branch.") },
            confirmButton = {
                Button(onClick = {
                    confirmDefaultPush = false
                    runGit("Pushed ${st.branch}", "git.push") { put("setUpstream", true); put("allowDefault", true) }
                }) { Text("Push") }
            },
            dismissButton = { TextButton(onClick = { confirmDefaultPush = false }) { Text("Cancel") } },
        )
    }
}
