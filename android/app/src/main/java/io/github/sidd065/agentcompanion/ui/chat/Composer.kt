package io.github.sidd065.agentcompanion.ui.chat

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.AlternateEmail
import androidx.compose.material.icons.outlined.AttachFile
import androidx.compose.material.icons.outlined.Bolt
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.Psychology
import androidx.compose.material.icons.outlined.Stop
import androidx.compose.material.icons.outlined.Tune
import androidx.compose.material.icons.automirrored.outlined.InsertDriveFile
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.CompleteHit
import io.github.sidd065.agentcompanion.ui.sessions.AttachmentChip
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.delay

data class CmdEntry(val name: String, val description: String, val local: Boolean)

/** Commands the app implements itself (mirroring agy's TUI-only slash commands). */
val localCommands = listOf(
    CmdEntry("model", "Switch model", true),
    CmdEntry("effort", "Change thinking level", true),
    CmdEntry("mode", "Change agent mode (ask first, accept edits, plan)", true),
    CmdEntry("new", "Start a new session in this folder", true),
    CmdEntry("clear", "Start a new session in this folder", true),
    CmdEntry("fork", "Branch this conversation into a new session", true),
    CmdEntry("rewind", "Roll the conversation (and files) back to an earlier message", true),
    CmdEntry("rename", "Rename this session", true),
    CmdEntry("diff", "Show uncommitted changes and agent edits", true),
    CmdEntry("context", "Context window and token usage", true),
    CmdEntry("usage", "Context window and token usage", true),
    CmdEntry("copy", "Copy the latest response", true),
    CmdEntry("export", "Export the conversation as Markdown", true),
    CmdEntry("artifacts", "Plans, task lists and other files agy saved", true),
    CmdEntry("files", "Browse this session's folder", true),
    CmdEntry("terminal", "Continue this conversation in the agy TUI", true),
    CmdEntry("stop", "Stop the agy process (it resumes on your next message)", true),
    CmdEntry("reload", "Reload the conversation from agy", true),
    CmdEntry("help", "List commands", true),
)

class ComposerState {
    var value by mutableStateOf(TextFieldValue(""))
    val attachments: SnapshotStateList<Pending> = mutableStateListOf()
    fun setText(t: String) { value = TextFieldValue(t, TextRange(t.length)) }
    fun clear() { value = TextFieldValue(""); attachments.clear() }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun Composer(
    state: ComposerState,
    busy: Boolean,
    delivery: String,
    enterSends: Boolean,
    modelLabel: String?,
    thoughtLabel: String?,
    modeLabel: String?,
    skipPerms: Boolean,
    commands: List<CmdEntry>,
    focus: FocusRequester,
    onSend: (String, List<Pending>, String?) -> Unit,
    onStop: () -> Unit,
    onPickPhoto: () -> Unit,
    onPickFile: () -> Unit,
    onBrowseHost: () -> Unit,
    onModel: () -> Unit,
    onThinking: () -> Unit,
    onMode: () -> Unit,
    onLocalCommand: (String) -> Unit,
    complete: suspend (String) -> List<CompleteHit>,
) {
    val ex = LocalExtra.current
    var plusMenu by remember { mutableStateOf(false) }
    val text = state.value.text
    val cursor = state.value.selection.start.coerceIn(0, text.length)
    // Slash command suggestions: "/xyz" as the first token.
    val slashQuery = if (text.startsWith("/") && !text.contains(' ') && !text.contains('\n')) text.drop(1) else null
    val slashHits = slashQuery?.let { q -> commands.filter { it.name.startsWith(q, true) || (q.length > 1 && it.name.contains(q, true)) }.take(30) } ?: emptyList()
    // @mention: token before the cursor starting with @.
    val before = text.substring(0, cursor)
    val atToken = Regex("""(?:^|\s)@([^\s@]*)$""").find(before)?.groupValues?.get(1)
    var fileHits by remember { mutableStateOf<List<CompleteHit>>(emptyList()) }
    LaunchedEffect(atToken) {
        if (atToken == null) { fileHits = emptyList(); return@LaunchedEffect }
        delay(200)
        fileHits = runCatching { complete(atToken) }.getOrDefault(emptyList())
    }
    val canSend = text.isNotBlank() || state.attachments.isNotEmpty()
    val defaultDelivery = if (delivery == "steer") "steer" else "queue"

    fun send(alt: Boolean) {
        if (!canSend) return
        val d = if (busy) (if (alt) (if (defaultDelivery == "queue") "steer" else "queue") else defaultDelivery) else null
        onSend(text, state.attachments.toList(), d)
    }

    Surface(color = MaterialTheme.colorScheme.surface, tonalElevation = 2.dp) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding()) {
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f))
            // Suggestions
            if (slashHits.isNotEmpty()) {
                Suggestions {
                    items(slashHits, key = { "c:" + it.name }) { c ->
                        SuggestionRow(if (c.local) Icons.Outlined.Tune else Icons.Outlined.Code, "/" + c.name, c.description) {
                            if (c.local) {
                                state.clear()
                                onLocalCommand(c.name)
                            } else {
                                state.setText("/" + c.name + " ")
                            }
                        }
                    }
                }
            } else if (atToken != null && fileHits.isNotEmpty()) {
                Suggestions {
                    items(fileHits, key = { "f:" + it.path }) { h ->
                        SuggestionRow(if (h.dir) Icons.Outlined.Folder else Icons.AutoMirrored.Outlined.InsertDriveFile, h.path, null) {
                            val start = before.length - atToken.length - 1
                            val newText = text.substring(0, start) + "@" + h.path + " " + text.substring(cursor)
                            state.value = TextFieldValue(newText, TextRange(start + h.path.length + 2))
                            if (!h.dir && state.attachments.none { it.path == h.abs }) {
                                state.attachments.add(Pending("path", h.path, path = h.abs))
                            }
                        }
                    }
                }
            }
            if (state.attachments.isNotEmpty()) {
                Row(Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 10.dp, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    state.attachments.toList().forEachIndexed { i, a -> AttachmentChip(a) { state.attachments.removeAt(i) } }
                }
            }
            // Config chips
            Row(
                Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 10.dp, vertical = 4.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically,
            ) {
                MiniChip(Icons.Outlined.Bolt, modelLabel ?: "Model", onModel)
                if (thoughtLabel != null) MiniChip(Icons.Outlined.Psychology, thoughtLabel, onThinking)
                MiniChip(Icons.Outlined.Tune, modeLabel ?: "Mode", onMode)
                if (skipPerms) Text("skip-permissions", style = MaterialTheme.typography.labelSmall, color = ex.warning, fontFamily = FontFamily.Monospace)
            }
            Row(Modifier.padding(start = 4.dp, end = 8.dp, bottom = 6.dp), verticalAlignment = Alignment.Bottom) {
                Box {
                    IconButton(onClick = { plusMenu = true }) { Icon(Icons.Outlined.Add, "Add") }
                    DropdownMenu(plusMenu, { plusMenu = false }) {
                        DropdownMenuItem(text = { Text("Photos") }, leadingIcon = { Icon(Icons.Outlined.Image, null) }, onClick = { plusMenu = false; onPickPhoto() })
                        DropdownMenuItem(text = { Text("File from phone") }, leadingIcon = { Icon(Icons.Outlined.AttachFile, null) }, onClick = { plusMenu = false; onPickFile() })
                        DropdownMenuItem(text = { Text("File on computer") }, leadingIcon = { Icon(Icons.Outlined.Folder, null) }, onClick = { plusMenu = false; onBrowseHost() })
                        DropdownMenuItem(text = { Text("Mention a file (@)") }, leadingIcon = { Icon(Icons.Outlined.AlternateEmail, null) }, onClick = {
                            plusMenu = false
                            val t = state.value.text
                            state.setText(if (t.isEmpty() || t.endsWith(" ") || t.endsWith("\n")) "$t@" else "$t @")
                            runCatching { focus.requestFocus() }
                        })
                        DropdownMenuItem(text = { Text("Commands (/)") }, leadingIcon = { Icon(Icons.Outlined.Code, null) }, onClick = {
                            plusMenu = false; state.setText("/"); runCatching { focus.requestFocus() }
                        })
                    }
                }
                TextField(
                    value = state.value,
                    onValueChange = { state.value = it },
                    modifier = Modifier.weight(1f).heightIn(min = 48.dp, max = 180.dp).focusRequester(focus),
                    placeholder = { Text(if (busy) (if (defaultDelivery == "queue") "Queue a message…" else "Steer the agent…") else "Message agy…") },
                    shape = RoundedCornerShape(22.dp),
                    colors = TextFieldDefaults.colors(
                        focusedIndicatorColor = Color.Transparent, unfocusedIndicatorColor = Color.Transparent,
                        focusedContainerColor = MaterialTheme.colorScheme.surfaceContainerHigh, unfocusedContainerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
                    ),
                    keyboardOptions = KeyboardOptions(imeAction = if (enterSends) ImeAction.Send else ImeAction.Default),
                    keyboardActions = KeyboardActions(onSend = { send(false) }),
                    singleLine = false,
                    maxLines = 8,
                )
                Spacer(Modifier.width(6.dp))
                val showStop = busy && !canSend
                Box(
                    Modifier.size(46.dp).clip(CircleShape)
                        .background(if (showStop) ex.danger.copy(alpha = 0.9f) else if (canSend) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surfaceContainerHighest)
                        .combinedClickable(
                            onClick = { if (showStop) onStop() else send(false) },
                            onLongClick = { if (!showStop) send(true) },
                        ),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(
                        if (showStop) Icons.Outlined.Stop else Icons.AutoMirrored.Filled.Send,
                        if (showStop) "Stop" else "Send",
                        tint = if (showStop || canSend) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun Suggestions(content: androidx.compose.foundation.lazy.LazyListScope.() -> Unit) {
    Surface(color = MaterialTheme.colorScheme.surfaceContainer) {
        LazyColumn(Modifier.fillMaxWidth().heightIn(max = 260.dp), content = content)
    }
}

@Composable
private fun SuggestionRow(icon: ImageVector, title: String, sub: String?, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 14.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium, fontFamily = FontFamily.Monospace, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (!sub.isNullOrBlank()) Text(sub, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
private fun MiniChip(icon: ImageVector, label: String, onClick: () -> Unit) {
    Row(
        Modifier.clip(RoundedCornerShape(50)).background(MaterialTheme.colorScheme.surfaceContainerHigh).clickable(onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 5.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(4.dp))
        Text(label, style = MaterialTheme.typography.labelMedium, maxLines = 1)
    }
}
