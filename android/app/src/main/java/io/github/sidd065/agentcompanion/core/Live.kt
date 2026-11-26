package io.github.sidd065.agentcompanion.core

import kotlinx.serialization.Serializable

/** What a conversation is doing beyond its transcript (daemon: core/live.go). */
@Serializable
data class LiveState(
    val tasks: List<LiveTask> = emptyList(),
    val artifacts: List<LiveArtifact> = emptyList(),
    val subagents: List<LiveSubagent> = emptyList(),
    val messages: List<LiveMessage> = emptyList(),
    val hook: String? = null,
    val running: Boolean = false,
) {
    val reviewable: List<LiveArtifact> get() = artifacts.filter { it.needsReview }
    val activeAgents: List<LiveSubagent> get() = subagents.filter { it.isWorking(running) }
    val isEmpty: Boolean get() = tasks.isEmpty() && artifacts.isEmpty() && subagents.isEmpty() && messages.isEmpty() && hook.isNullOrBlank()
}

/** A background shell command. */
@Serializable
data class LiveTask(
    val id: String,
    val tool: String = "",
    val summary: String = "",
    val description: String = "",
    val start: Long = 0,
    val step: Int = 0,
    val log: String = "",
    val logSize: Long = 0,
    val daemon: Boolean = false,
) {
    val title: String get() = summary.ifBlank { description.lineSequence().firstOrNull().orEmpty() }.ifBlank { id.substringAfterLast('/') }
}

@Serializable
data class LiveArtifact(
    val name: String,
    val path: String = "",
    val uri: String = "",
    val kind: String = "file",
    val summary: String? = null,
    val size: Long = 0,
    val updated: Long = 0,
    val version: Long = 0,
    val requestFeedback: Boolean = false,
    val userFacing: Boolean = false,
    val review: String? = null,
    val url: String? = null,
    /** The user wrote to the agent after this version (agentc 0.3.3+). */
    val answered: Boolean = false,
) {
    /** agy asked for feedback and nobody has answered or approved it yet. */
    val needsReview: Boolean get() = requestFeedback && !answered && review != "APPROVED" && review != "ACCEPTED"
}

@Serializable
data class LiveSubagent(
    val id: String,
    val role: String = "",
    val type: String? = null,
    val state: String = "",
    val spawnStep: Int = 0,
    val updated: Long = 0,
) {
    val alive: Boolean get() = isWorking(sessionRunning = true)

    fun isWorking(sessionRunning: Boolean = true, nowMs: Long = System.currentTimeMillis()): Boolean = when {
        state.equals("RUNNING", true) || state.equals("WORKING", true) -> true
        state.equals("DONE", true) || state.equals("COMPLETED", true) ||
            state.equals("STOPPED", true) || state.equals("KILLED", true) || state.equals("FAILED", true) -> false
        state.equals("ALIVE", true) -> sessionRunning && updated > 0L && (nowMs - updated) in 0L..90_000L
        else -> false
    }

    fun displayState(sessionRunning: Boolean = true, nowMs: Long = System.currentTimeMillis()): String = when {
        isWorking(sessionRunning, nowMs) -> "working"
        state.equals("ALIVE", true) || state.equals("DONE", true) || state.equals("COMPLETED", true) || state.isBlank() -> "done"
        else -> state.lowercase()
    }
}

@Serializable
data class LiveMessage(
    val id: String,
    val sender: String = "",
    val title: String = "",
    val content: String = "",
    val ts: Long = 0,
    val read: Boolean = false,
    /** task (a background command's result), user (your queued message), notice (from agy) or "" (a subagent). */
    val kind: String = "",
) {
    /** What this message is, also for agentc versions that don't say. */
    val effectiveKind: String get() = kind.ifBlank { if (content.startsWith("Task id ")) "task" else "" }
}

/** One step of a subagent transcript. */
@Serializable
data class SubStep(
    val i: Int = 0,
    val type: String = "",
    val status: String = "",
    val source: String? = null,
    val ts: Long = 0,
    val text: String? = null,
    val thinking: String? = null,
    val tools: List<SubTool> = emptyList(),
    val model: String? = null,
)

@Serializable
data class SubTool(val name: String, val args: String = "")

/** Feedback on an artifact, file or diff line (daemon: core/comments.go). */
@Serializable
data class DraftComment(
    val kind: String, // artifact | file | diff
    val uri: String,
    val startLine: Int = 0,
    val endLine: Int = 0,
    val quote: String = "",
    val comment: String = "",
    val approve: Boolean = false,
)

private val ansi = Regex("\u001B\\[[0-9;?]*[ -/]*[@-~]|\u001B[()][AB012]")

/** Terminal output → readable text (drops ANSI codes, resolves carriage returns). */
fun cleanTerminalText(raw: String): String =
    raw.replace(ansi, "").replace("\r\n", "\n").lineSequence().joinToString("\n") { it.substringAfterLast('\r') }
