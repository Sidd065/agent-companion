package io.github.sidd065.agentcompanion.ui.chat

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.CheckBox
import androidx.compose.material.icons.outlined.CheckBoxOutlineBlank
import androidx.compose.material.icons.outlined.ExpandLess
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.HelpOutline
import androidx.compose.material.icons.outlined.RadioButtonChecked
import androidx.compose.material.icons.outlined.RadioButtonUnchecked
import androidx.compose.material.icons.outlined.RateReview
import androidx.compose.material.icons.outlined.TaskAlt
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
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
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.Item
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.components.MarkdownText
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.put

/** One question of Claude's AskUserQuestion tool. */
data class ClaudeQuestion(val question: String, val header: String, val options: List<Pair<String, String>>, val multi: Boolean)

fun parseClaudeQuestions(input: kotlinx.serialization.json.JsonElement?): List<ClaudeQuestion> =
    input.obj()?.get("questions").arr()?.mapNotNull { q ->
        val o = q.obj() ?: return@mapNotNull null
        ClaudeQuestion(
            o.str("question") ?: return@mapNotNull null,
            o.str("header").orEmpty(),
            o["options"].arr()?.mapNotNull { op -> op.obj()?.let { (it.str("label") ?: return@mapNotNull null) to it.str("description").orEmpty() } } ?: emptyList(),
            (o["multiSelect"] as? JsonPrimitive)?.booleanOrNull == true,
        )
    } ?: emptyList()

private fun answerOf(item: Item): Map<String, String> {
    val a = item.perm?.answer.obj()?.get("answers").obj() ?: return emptyMap()
    return a.mapNotNull { (k, v) -> (v as? JsonPrimitive)?.contentOrNull?.let { k to it } }.toMap()
}

private fun messageOf(item: Item): String? = item.perm?.answer.obj()?.get("message")?.let { (it as? JsonPrimitive)?.contentOrNull }

/** Claude asks the user questions (AskUserQuestion): pick options or type an answer. */
@Composable
fun ClaudeQuestionCard(item: Item, actions: TranscriptActions) {
    val p = item.perm ?: return
    val ex = LocalExtra.current
    val scope = rememberCoroutineScope()
    val qs = remember(p.input) { parseClaudeQuestions(p.input) }
    val pending = item.state == "pending"
    var idx by remember { mutableStateOf(0) }
    val picked = remember { mutableStateMapOf<Int, List<String>>() }
    val other = remember { mutableStateMapOf<Int, String>() }
    var busy by remember { mutableStateOf(false) }
    Surface(
        Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
        shape = RoundedCornerShape(14.dp),
        color = if (pending) MaterialTheme.colorScheme.secondaryContainer.copy(alpha = 0.35f) else MaterialTheme.colorScheme.surfaceContainer,
        border = BorderStroke(1.dp, if (pending) MaterialTheme.colorScheme.secondary.copy(alpha = 0.5f) else ex.cardBorder),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.HelpOutline, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.secondary)
                Spacer(Modifier.width(8.dp))
                Text(if (pending) "${actions.label} has ${if (qs.size == 1) "a question" else "${qs.size} questions"} for you" else "Questions",
                    style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                if (pending && qs.size > 1) Text("${idx + 1} of ${qs.size}", style = MaterialTheme.typography.labelSmall, color = ex.muted)
            }
            if (!pending) {
                val ans = answerOf(item).ifEmpty { messageOf(item)?.let { m -> qs.associate { it.question to m } } ?: emptyMap() }
                qs.forEach { q ->
                    Text(q.question, style = MaterialTheme.typography.bodySmall, color = ex.muted)
                    Text(ans[q.question]?.let { "→ $it" } ?: if (item.state == "cancelled") "(cancelled)" else "(skipped)",
                        style = MaterialTheme.typography.bodyMedium, color = if (ans[q.question] != null) MaterialTheme.colorScheme.primary else ex.muted)
                }
                return@Column
            }
            if (qs.isEmpty()) {
                Text(p.title ?: "Question", style = MaterialTheme.typography.bodyMedium)
                return@Column
            }
            if (qs.size > 1) {
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    qs.forEachIndexed { i, q ->
                        val done = !picked[i].isNullOrEmpty() || !other[i].isNullOrBlank()
                        FilterChip(i == idx, onClick = { idx = i }, label = { Text((q.header.ifBlank { "Q${i + 1}" }) + if (done) " ✓" else "") })
                    }
                }
            }
            val q = qs[idx]
            if (qs.size == 1 && q.header.isNotBlank()) Text(q.header.uppercase(), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.secondary)
            Text(q.question, style = MaterialTheme.typography.bodyLarge)
            if (q.multi) Text("Select all that apply", style = MaterialTheme.typography.labelSmall, color = ex.muted)
            q.options.forEach { (label, desc) ->
                val sel = picked[idx]?.contains(label) == true
                Row(
                    Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp))
                        .border(1.dp, if (sel) MaterialTheme.colorScheme.primary else ex.cardBorder, RoundedCornerShape(10.dp))
                        .clickable {
                            val cur = picked[idx].orEmpty()
                            picked[idx] = if (q.multi) (if (sel) cur - label else cur + label) else listOf(label)
                            if (!q.multi) other.remove(idx)
                        }.padding(horizontal = 10.dp, vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        when {
                            q.multi && sel -> Icons.Outlined.CheckBox
                            q.multi -> Icons.Outlined.CheckBoxOutlineBlank
                            sel -> Icons.Outlined.RadioButtonChecked
                            else -> Icons.Outlined.RadioButtonUnchecked
                        }, null, Modifier.size(18.dp), tint = if (sel) MaterialTheme.colorScheme.primary else ex.muted,
                    )
                    Spacer(Modifier.width(10.dp))
                    Column(Modifier.weight(1f)) {
                        Text(label, style = MaterialTheme.typography.bodyMedium)
                        if (desc.isNotBlank()) Text(desc, style = MaterialTheme.typography.bodySmall, color = ex.muted)
                    }
                }
            }
            OutlinedTextField(other[idx] ?: "", { v -> other[idx] = v; if (!q.multi && v.isNotBlank()) picked.remove(idx) },
                placeholder = { Text("Other…") }, modifier = Modifier.fillMaxWidth())
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                TextButton(enabled = !busy, onClick = {
                    busy = true
                    scope.launch {
                        runCatching { actions.conn.rpc.call("session.permission", params { put("id", actions.sessionId); put("reqId", p.reqId); put("optionId", "deny") }) }
                        busy = false
                    }
                }) { Text("Skip") }
                Spacer(Modifier.weight(1f))
                if (idx > 0) TextButton(onClick = { idx-- }) { Text("Back") }
                val answered = !picked[idx].isNullOrEmpty() || !other[idx].isNullOrBlank()
                if (idx < qs.size - 1) {
                    Button(onClick = { idx++ }, enabled = answered) { Text("Next") }
                } else {
                    val all = qs.indices.all { !picked[it].isNullOrEmpty() || !other[it].isNullOrBlank() }
                    Button(enabled = all && !busy, onClick = {
                        busy = true
                        val answers = JsonObject(qs.mapIndexed { i, qq ->
                            val parts = picked[i].orEmpty() + listOfNotNull(other[i]?.trim()?.takeIf { it.isNotEmpty() })
                            qq.question to JsonPrimitive(parts.joinToString(", "))
                        }.toMap())
                        scope.launch {
                            runCatching {
                                actions.conn.rpc.call("session.permission", params {
                                    put("id", actions.sessionId); put("reqId", p.reqId); put("optionId", "answer"); put("answers", answers)
                                })
                            }
                            busy = false
                        }
                    }) { Text("Submit") }
                }
            }
        }
    }
}

/** Claude finished planning (ExitPlanMode): approve how to proceed, or send it back with feedback. */
@Composable
fun ClaudePlanCard(item: Item, actions: TranscriptActions) {
    val p = item.perm ?: return
    val ex = LocalExtra.current
    val scope = rememberCoroutineScope()
    val pending = item.state == "pending"
    val plan = p.input.str("plan").orEmpty()
    val planPath = p.input.str("planFilePath")
    var expanded by remember { mutableStateOf(pending) }
    var feedback by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    fun answer(option: String, message: String? = null) {
        busy = true
        scope.launch {
            runCatching {
                actions.conn.rpc.call("session.permission", params {
                    put("id", actions.sessionId); put("reqId", p.reqId); put("optionId", option)
                    if (message != null) put("message", message)
                })
            }
            busy = false
        }
    }
    Surface(
        Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
        shape = RoundedCornerShape(14.dp),
        color = MaterialTheme.colorScheme.surfaceContainer,
        border = BorderStroke(1.dp, if (pending) MaterialTheme.colorScheme.primary.copy(alpha = 0.6f) else ex.cardBorder),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.clickable { expanded = !expanded }) {
                Icon(Icons.Outlined.TaskAlt, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    Text(if (pending) "Plan ready for review" else "Plan", style = MaterialTheme.typography.titleSmall)
                    plan.lineSequence().firstOrNull { it.isNotBlank() }?.let { plainPreview(it) }?.let {
                        Text(it, style = MaterialTheme.typography.bodySmall, color = ex.muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
                Icon(if (expanded) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore, null)
            }
            if (expanded && plan.isNotBlank()) {
                Box(Modifier.fillMaxWidth().heightIn(max = 460.dp).clip(RoundedCornerShape(8.dp)).border(1.dp, ex.cardBorder, RoundedCornerShape(8.dp))
                    .verticalScroll(rememberScrollState()).padding(10.dp)) {
                    MarkdownText(plan)
                }
            }
            if (!pending) {
                val chosen = p.options.firstOrNull { it.optionId == p.chosen }
                val msg = messageOf(item)
                Text(
                    when {
                        item.state == "cancelled" -> "Cancelled"
                        chosen == null -> "Answered"
                        chosen.optionId.startsWith("plan_") -> "Approved · " + chosen.name.removePrefix("Yes, and ").removePrefix("Yes, ")
                        else -> "Sent back for changes"
                    },
                    style = MaterialTheme.typography.labelMedium,
                    color = if (chosen?.optionId?.startsWith("plan_") == true) ex.success else ex.muted,
                )
                if (!msg.isNullOrBlank()) Text(parseFeedback(msg)?.joinToString("\n") { (f, c) -> "$f: $c" } ?: msg,
                    style = MaterialTheme.typography.bodySmall, color = ex.muted, maxLines = 6)
                return@Column
            }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                p.options.filter { it.optionId.startsWith("plan_") }.forEachIndexed { i, o ->
                    val label = o.name.removePrefix("Yes, and ").removePrefix("Yes, ").replaceFirstChar { it.uppercase() }
                    if (i == 0) Button(onClick = { answer(o.optionId) }, enabled = !busy) { Text("Approve · $label") }
                    else OutlinedButton(onClick = { answer(o.optionId) }, enabled = !busy) { Text("Approve · $label") }
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (planPath != null) TextButton(onClick = { actions.onOpenFile(planPath) }) {
                    Icon(Icons.Outlined.RateReview, null, Modifier.size(16.dp)); Spacer(Modifier.width(6.dp)); Text("Comment on plan")
                }
                TextButton(onClick = { feedback = "" }, enabled = !busy) { Text("Keep planning…") }
            }
        }
    }
    feedback?.let { note ->
        AlertDialog(
            onDismissRequest = { feedback = null },
            title = { Text("Keep planning") },
            text = {
                OutlinedTextField(note, { feedback = it }, Modifier.fillMaxWidth(), placeholder = { Text("What should ${actions.label} change in the plan?") }, minLines = 3)
            },
            confirmButton = {
                TextButton(enabled = note.isNotBlank(), onClick = { feedback = null; answer("deny", note.trim()) }) { Text("Send back") }
            },
            dismissButton = { TextButton(onClick = { feedback = null }) { Text("Cancel") } },
        )
    }
}
