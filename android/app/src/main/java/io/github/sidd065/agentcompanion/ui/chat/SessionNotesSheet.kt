package io.github.sidd065.agentcompanion.ui.chat

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.StickyNote2
import androidx.compose.material.icons.outlined.AddTask
import androidx.compose.material.icons.outlined.CheckBox
import androidx.compose.material.icons.outlined.CheckBoxOutlineBlank
import androidx.compose.material.icons.outlined.DeleteOutline
import androidx.compose.material.icons.outlined.Input
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.SessionNote
import io.github.sidd065.agentcompanion.ui.components.AppSheet
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra

data class NoteChecklistItem(val lineIndex: Int, val checked: Boolean, val text: String)

private val checklistLineRegex = Regex("""^(\s*[-*+]\s+\[)([ xX])(\]\s+)(.*)$""")

fun sessionNoteKey(hostId: String, sessionId: String): String =
    "${hostId.trim()}:${sessionId.trim()}"

fun parseNoteChecklist(text: String): List<NoteChecklistItem> {
    if (text.isBlank()) return emptyList()
    return text.replace("\r\n", "\n").lines().mapIndexedNotNull { idx, line ->
        val m = checklistLineRegex.find(line) ?: return@mapIndexedNotNull null
        NoteChecklistItem(
            lineIndex = idx,
            checked = m.groupValues[2].equals("x", ignoreCase = true),
            text = m.groupValues[4].trim(),
        )
    }
}

fun toggleNoteChecklistLine(text: String, lineIndex: Int): String {
    val lines = text.replace("\r\n", "\n").lines().toMutableList()
    if (lineIndex !in lines.indices) return text
    val m = checklistLineRegex.find(lines[lineIndex]) ?: return text
    val nextMark = if (m.groupValues[2].equals("x", ignoreCase = true)) " " else "x"
    lines[lineIndex] = m.groupValues[1] + nextMark + m.groupValues[3] + m.groupValues[4]
    return lines.joinToString("\n")
}

fun nextNoteSummary(text: String): String {
    if (text.isBlank()) return ""
    val checklist = parseNoteChecklist(text)
    val nextTodo = checklist.firstOrNull { !it.checked }
    if (nextTodo != null && nextTodo.text.isNotBlank()) return nextTodo.text
    return text.lineSequence()
        .map { it.trim().trimStart('#', '-', '*', '>', ' ').trim() }
        .firstOrNull { it.isNotBlank() }
        .orEmpty()
}

@Composable
fun SessionNotesBanner(
    noteText: String,
    onOpen: () -> Unit,
    onToggleTopTodo: (() -> Unit)? = null,
) {
    val summary = remember(noteText) { nextNoteSummary(noteText) }
    if (summary.isBlank()) return
    val checklist = remember(noteText) { parseNoteChecklist(noteText) }
    val firstUnchecked = checklist.firstOrNull { !it.checked }
    val ex = LocalExtra.current

    Surface(
        color = MaterialTheme.colorScheme.secondaryContainer.copy(alpha = 0.45f),
        shape = RoundedCornerShape(10.dp),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 10.dp, vertical = 3.dp)
            .clickable(onClick = onOpen),
    ) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (firstUnchecked != null && onToggleTopTodo != null) {
                IconButton(onClick = onToggleTopTodo, modifier = Modifier.size(24.dp)) {
                    Icon(Icons.Outlined.CheckBoxOutlineBlank, "Mark next note item done", Modifier.size(16.dp), tint = MaterialTheme.colorScheme.secondary)
                }
                Spacer(Modifier.width(4.dp))
            } else {
                Icon(Icons.AutoMirrored.Outlined.StickyNote2, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.secondary)
                Spacer(Modifier.width(6.dp))
            }
            Text(
                "Next: $summary",
                style = MaterialTheme.typography.labelMedium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            if (checklist.isNotEmpty()) {
                Spacer(Modifier.width(6.dp))
                Text(
                    "${checklist.count { it.checked }}/${checklist.size}",
                    style = MaterialTheme.typography.labelSmall,
                    color = ex.muted,
                )
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionNotesSheet(
    note: SessionNote?,
    sessionTitle: String,
    onSave: (String) -> Unit,
    onInsertIntoComposer: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var draft by remember(note?.text) { mutableStateOf(note?.text.orEmpty()) }
    val checklist = remember(draft) { parseNoteChecklist(draft) }
    val ex = LocalExtra.current

    fun updateDraft(next: String) {
        draft = next
        onSave(next)
    }

    AppSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(
            Modifier
                .fillMaxWidth()
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp)
                .padding(bottom = 20.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("Session notes", style = MaterialTheme.typography.titleMedium)
                    Text(
                        buildString {
                            append("Stored privately on this device")
                            if (note != null && note.updatedAt > 0L && draft.isNotBlank()) {
                                append(" · updated ")
                                append(relTime(note.updatedAt))
                            }
                        },
                        style = MaterialTheme.typography.labelSmall,
                        color = ex.muted,
                    )
                }
                Button(onClick = { onSave(draft); onDismiss() }) { Text("Done") }
            }

            if (sessionTitle.isNotBlank()) {
                Text(
                    sessionTitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }

            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                AssistChip(
                    onClick = {
                        val prefix = if (draft.isBlank() || draft.endsWith("\n")) "" else "\n"
                        updateDraft(draft + prefix + "- [ ] ")
                    },
                    leadingIcon = { Icon(Icons.Outlined.AddTask, null, Modifier.size(16.dp)) },
                    label = { Text("Add to-do") },
                )
                if (draft.isNotBlank()) {
                    AssistChip(
                        onClick = {
                            val nextItem = nextNoteSummary(draft).ifBlank { draft.trim() }
                            onInsertIntoComposer(nextItem)
                            onDismiss()
                        },
                        leadingIcon = { Icon(Icons.Outlined.Input, null, Modifier.size(16.dp)) },
                        label = { Text("Insert next into message") },
                    )
                    AssistChip(
                        onClick = { updateDraft("") },
                        leadingIcon = { Icon(Icons.Outlined.DeleteOutline, null, Modifier.size(16.dp)) },
                        label = { Text("Clear") },
                    )
                }
            }

            if (checklist.isNotEmpty()) {
                Surface(
                    color = MaterialTheme.colorScheme.surfaceContainer,
                    shape = RoundedCornerShape(10.dp),
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Column(Modifier.padding(10.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text(
                            "Checklist (${checklist.count { it.checked }}/${checklist.size} done)",
                            style = MaterialTheme.typography.labelSmall,
                            color = ex.muted,
                        )
                        checklist.forEach { item ->
                            Row(
                                Modifier
                                    .fillMaxWidth()
                                    .clickable { updateDraft(toggleNoteChecklistLine(draft, item.lineIndex)) }
                                    .padding(vertical = 3.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Icon(
                                    if (item.checked) Icons.Outlined.CheckBox else Icons.Outlined.CheckBoxOutlineBlank,
                                    contentDescription = if (item.checked) "Completed" else "Pending",
                                    modifier = Modifier.size(18.dp),
                                    tint = if (item.checked) ex.success else MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                                Spacer(Modifier.width(8.dp))
                                Text(
                                    item.text.ifBlank { "(empty item)" },
                                    style = MaterialTheme.typography.bodySmall.copy(
                                        textDecoration = if (item.checked) TextDecoration.LineThrough else TextDecoration.None,
                                    ),
                                    color = if (item.checked) ex.muted else MaterialTheme.colorScheme.onSurface,
                                )
                            }
                        }
                    }
                }
            }

            OutlinedTextField(
                value = draft,
                onValueChange = ::updateDraft,
                modifier = Modifier.fillMaxWidth().heightIn(min = 150.dp, max = 320.dp),
                placeholder = { Text("Write notes on what you want to do next in this session…\nTip: use '- [ ] task' for interactive checklists.") },
                minLines = 6,
                maxLines = 14,
            )
            HorizontalDivider(color = androidx.compose.ui.graphics.Color.Transparent)
        }
    }
}
