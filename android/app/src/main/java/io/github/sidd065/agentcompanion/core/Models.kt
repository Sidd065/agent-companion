package io.github.sidd065.agentcompanion.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement

val AppJson = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    encodeDefaults = true
    isLenient = true
    coerceInputValues = true
}

@Serializable
data class HostConfig(
    /** The daemon's host id (derived from its identity key). */
    val id: String,
    val name: String,
    /** Base URL, normally https://<random>.trycloudflare.com (changes when the tunnel restarts). */
    val url: String,
    /** The daemon's static X25519 public key (base64url), learned from the pairing QR code. */
    val hostKey: String,
    /** This phone's key for this host: public half, and the private half wrapped by the Android Keystore. */
    val devicePub: String,
    val devicePrivWrapped: String,
    val deviceId: String = "",
    val fingerprint: String = "",
    val pairedAt: Long = 0,
    val notify: Boolean = true,
) {
    val label: String get() = name.ifBlank { url.removePrefix("https://").substringBefore('.') }
}

@Serializable
data class Usage(
    val inputTokens: Long = 0,
    val outputTokens: Long = 0,
    val cachedReadTokens: Long = 0,
    val cachedWriteTokens: Long = 0,
    val thoughtTokens: Long = 0,
    val totalTokens: Long = 0,
    /** Spend reported by the agent (Claude Code), in US dollars. */
    val costUsd: Double = 0.0,
)

@Serializable
data class SessionSummary(
    val id: String,
    val agent: String = "agy",
    val title: String = "",
    val cwd: String = "",
    val state: String = "",
    val live: Boolean = false,
    val known: Boolean = false,
    val attention: String? = null,
    val pendingPermissions: Int = 0,
    val queued: Int = 0,
    val model: String? = null,
    val modelName: String? = null,
    val mode: String? = null,
    val thought: String? = null,
    val skipPermissions: Boolean = true,
    val createdAt: Long = 0,
    val updatedAt: Long = 0,
    val lastText: String? = null,
    val contextUsed: Long = 0,
    val contextSize: Long = 0,
    val usage: Usage = Usage(),
    val turns: Int = 0,
    val archived: Boolean = false,
    val pinned: Boolean = false,
    val forkedFrom: String? = null,
    val origin: String? = null,
    val error: String? = null,
    val pid: Int = 0,
    /** Transient agent status, e.g. "compacting" (Claude Code). */
    val status: String? = null,
    /** Predicted next prompt (Claude Code). */
    val suggestion: String? = null,
    /** Set when another Claude Code process on the computer has this conversation open (its status). */
    val liveElsewhere: String? = null,
    /** When the running turn began (ms); also set for turns the agent started on its own. */
    val turnStart: Long = 0,
) {
    val busy: Boolean get() = state == "running" || state == "starting" || state == "waiting"
    val needsInput: Boolean get() = !attention.isNullOrEmpty() || state == "waiting"
    val displayTitle: String get() = title.ifBlank { "Untitled conversation" }
    val isClaude: Boolean get() = agent == "claude"
    /** How the agent is named in messages ("agy", "Claude"). */
    val agentLabel: String get() = agentLabel(agent)
}

fun agentLabel(agent: String?): String = when (agent) {
    "claude" -> "Claude"
    "codex" -> "Codex"
    else -> "agy"
}

fun agentProduct(agent: String?): String = when (agent) {
    "claude" -> "Claude Code"
    "codex" -> "Codex"
    else -> "agy"
}

@Serializable
data class ModelInfo(val modelId: String, val name: String = "", val description: String? = null)

@Serializable
data class Models(val availableModels: List<ModelInfo> = emptyList(), val currentModelId: String? = null)

@Serializable
data class ModeInfo(val id: String, val name: String = "", val description: String? = null)

@Serializable
data class Modes(val availableModes: List<ModeInfo> = emptyList(), val currentModeId: String? = null)

@Serializable
data class ConfigValue(val value: String, val name: String = "", val description: String? = null)

@Serializable
data class ConfigOption(
    val id: String,
    val name: String = "",
    val category: String? = null,
    val type: String = "select",
    val currentValue: String? = null,
    val options: List<ConfigValue> = emptyList(),
)

@Serializable
data class SlashCommand(val name: String, val description: String = "")

@Serializable
data class SessionExtras(
    val addDirs: List<String> = emptyList(),
    val models: Models? = null,
    val modes: Modes? = null,
    val configOptions: List<ConfigOption> = emptyList(),
    val commands: List<SlashCommand> = emptyList(),
    /** Agent details (Claude Code: plan, version, output style, agents). */
    val info: JsonElement? = null,
    val resumeAt: String? = null,
)

@Serializable
data class Block(
    val type: String,
    val mime: String? = null,
    val uri: String? = null,
    val name: String? = null,
    val text: String? = null,
)

@Serializable
data class Tool(
    val callId: String,
    val name: String? = null,
    val kind: String? = null,
    val title: String? = null,
    val status: String? = null,
    val locations: JsonElement? = null,
    val input: JsonElement? = null,
    val output: JsonElement? = null,
    val content: JsonElement? = null,
    val startTs: Long = 0,
    val endTs: Long = 0,
)

@Serializable
data class PermOption(val optionId: String, val name: String = "", val kind: String = "")

@Serializable
data class Perm(
    val reqId: String,
    val callId: String? = null,
    val title: String? = null,
    val toolKind: String? = null,
    val options: List<PermOption> = emptyList(),
    val chosen: String? = null,
    /** Claude Code: tool name, request kind (tool|question|plan), its input (questions/plan), reason and the answer given. */
    val tool: String? = null,
    val kind: String? = null,
    val input: JsonElement? = null,
    val reason: String? = null,
    val blockedPath: String? = null,
    val subagent: Boolean = false,
    val answer: JsonElement? = null,
)

@Serializable
data class Turn(
    val stopReason: String = "",
    val usage: Usage? = null,
    val durationMs: Long = 0,
    val error: String? = null,
)

@Serializable
data class Item(
    val id: String,
    val kind: String,
    val seq: Long = 0,
    val ts: Long = 0,
    val text: String? = null,
    val blocks: List<Block>? = null,
    val state: String? = null,
    val tool: Tool? = null,
    val plan: JsonElement? = null,
    val perm: Perm? = null,
    val level: String? = null,
    val turn: Turn? = null,
    val trunc: Boolean = false,
    /** The agent's id for this message (Claude message uuid). */
    val ref: String? = null,
)

@Serializable
data class Op(
    val seq: Long = 0,
    val op: String,
    val id: String? = null,
    val item: Item? = null,
    val text: String? = null,
    val items: List<Item>? = null,
)

@Serializable
data class AgentNotification(
    val id: String = "",
    val seq: Long = 0,
    val sessionId: String? = null,
    val termId: String? = null,
    val kind: String = "",
    val title: String = "",
    val body: String = "",
    val reqId: String? = null,
    val options: List<PermOption> = emptyList(),
    val ts: Long = 0,
)

@Serializable
data class Term(
    val id: String,
    val name: String = "",
    val kind: String = "shell",
    val cwd: String = "",
    val command: String = "",
    val created: Long = 0,
    val attached: Int = 0,
    val width: Int = 0,
    val height: Int = 0,
    val state: String? = null,
    val msg: String? = null,
    val origin: String? = null,
    val conversationId: String? = null,
    val currentCommand: String? = null,
    val dead: Boolean = false,
)

@Serializable
data class FsEntry(
    val name: String,
    val path: String,
    val type: String,
    val size: Long = 0,
    val mtime: Long = 0,
    val mode: String = "",
    val hidden: Boolean = false,
    val target: String? = null,
    val targetType: String? = null,
    val mime: String? = null,
    val children: Int = 0,
) {
    val isDir: Boolean get() = type == "dir" || targetType == "dir"
}

@Serializable
data class FsList(
    val path: String,
    val parent: String = "",
    val entries: List<FsEntry> = emptyList(),
    val gitBranch: String = "",
)

@Serializable
data class ReadResult(
    val path: String,
    val size: Long = 0,
    val mtime: Long = 0,
    val text: String? = null,
    val base64: String? = null,
    val binary: Boolean = false,
    val truncated: Boolean = false,
    val mime: String? = null,
)

@Serializable
data class AgentProfile(
    val id: String,
    val name: String = "",
    val command: String = "",
    val version: String? = null,
    val available: Boolean = false,
    val planned: Boolean = false,
    val reason: String? = null,
    val features: List<String> = emptyList(),
)

@Serializable
data class CompleteHit(val path: String, val dir: Boolean = false, val abs: String = "")

@Serializable
data class GitFile(val path: String, val status: String = "", val added: Int = 0, val removed: Int = 0)

@Serializable
data class GitDiff(
    val repo: Boolean = false,
    val root: String? = null,
    val branch: String? = null,
    val files: List<GitFile> = emptyList(),
    val diff: String = "",
    val truncated: Boolean = false,
    // git.show (one commit): what it is.
    val sha: String? = null,
    val subject: String? = null,
)

@Serializable
data class GitCommit(
    val sha: String,
    val short: String = "",
    val subject: String = "",
    val author: String = "",
    val time: Long = 0,
    val files: Int = 0,
    val added: Int = 0,
    val removed: Int = 0,
    /** On this branch but not on its upstream or main: what was added here. */
    val mine: Boolean = false,
)

@Serializable
data class GitLog(
    val repo: Boolean = false,
    val root: String? = null,
    val branch: String? = null,
    val commits: List<GitCommit> = emptyList(),
    /** What the "mine" commits are missing from: the upstream (e.g. origin/main) or main/master. */
    val base: String? = null,
    /** [base] is the branch's upstream, so those commits are unpushed. */
    val upstream: Boolean = false,
)

@Serializable
data class AgentFileDiff(
    val uri: String = "",
    val originalContent: String? = null,
    val modifiedContent: String? = null,
    val isArtifactFile: Boolean = false,
    val hasModelEdited: Boolean = false,
    val truncated: Boolean = false,
)

@Serializable
data class Artifact(val name: String, val path: String, val size: Long = 0, val mtime: Long = 0)

@Serializable
data class CmdResult(
    val args: List<String> = emptyList(),
    val stdout: String = "",
    val stderr: String = "",
    val exitCode: Int = 0,
    val durationMs: Long = 0,
)

@Serializable
data class McpServer(
    val name: String,
    val command: String? = null,
    val args: List<String>? = null,
    val env: Map<String, String>? = null,
    val url: String? = null,
    val headers: Map<String, String>? = null,
    val type: String? = null,
    val disabled: Boolean = false,
)

@Serializable
data class McpList(val servers: List<McpServer> = emptyList(), val path: String = "", val cli: String? = null, val error: String? = null)

@Serializable
data class ConfigFileInfo(
    val name: String,
    val path: String,
    val desc: String = "",
    val exists: Boolean = false,
    val size: Long = 0,
    val dir: Boolean = false,
)

@Serializable
data class SkillInfo(val name: String, val description: String = "", val path: String = "", val source: String = "")

@Serializable
data class AgyModel(val id: String, val name: String = "", val default: Boolean = false)

/**
 * agy launch flags chosen on the phone. Saved in app settings and applied to
 * every new chat session (`agy acp …`), agy terminal (`agy …`) and adopted
 * conversation.
 */
@Serializable
data class AgyFlags(
    val skipPermissions: Boolean = true,
    val sandbox: Boolean = false,
    val model: String = "",
    val effort: String = "",
    val mode: String = "",
    val agent: String = "",
    val addDirs: List<String> = emptyList(),
    val disableTelemetry: Boolean = false,
    val includeDefaultCustomizations: Boolean = true,
    val acpExtra: String = "",
    // Terminal (TUI) only
    val project: String = "",
    val newProject: Boolean = false,
    val remoteControl: Boolean = false,
    val remoteAgent: Boolean = false,
    val settingsJson: String = "",
    val tuiExtra: String = "",
) {
    /** Flags for `agy` in a terminal (without --dangerously-skip-permissions, sent separately). */
    fun tuiArgs(): List<String> = buildList {
        if (sandbox) add("--sandbox")
        if (model.isNotBlank()) { add("--model"); add(model.trim()) }
        if (effort.isNotBlank()) { add("--effort"); add(effort.trim()) }
        // The TUI's --mode takes accept-edits|auto|plan; "default" means no flag.
        if (mode.isNotBlank() && mode != "default") { add("--mode"); add(mode.trim()) }
        if (agent.isNotBlank()) { add("--agent"); add(agent.trim()) }
        addDirs.filter { it.isNotBlank() }.forEach { add("--add-dir"); add(it) }
        if (project.isNotBlank()) { add("--project"); add(project.trim()) }
        if (newProject) add("--new-project")
        if (remoteControl) add("--remote-control")
        if (remoteAgent) add("--remote-agent")
        if (settingsJson.isNotBlank()) { add("--settings"); add(settingsJson.trim()) }
        addAll(splitArgs(tuiExtra))
    }

    /**
     * Terminal flags for continuing an existing conversation: only how the process
     * runs. Model, effort, mode, agent and project stay as the conversation has them
     * (matching what the daemon does when a chat session resumes a past conversation).
     */
    fun tuiResumeArgs(): List<String> =
        copy(model = "", effort = "", mode = "", agent = "", project = "", newProject = false, remoteAgent = false).tuiArgs()

    /** Flags for `agy acp` (chat sessions); model/mode/effort are applied over ACP. */
    fun acpArgs(): List<String> = buildList {
        if (skipPermissions) add("--dangerously-skip-permissions")
        if (sandbox) add("--sandbox")
        if (agent.isNotBlank()) { add("--agent"); add(agent.trim()) }
        addDirs.filter { it.isNotBlank() }.forEach { add("--add-dir"); add(it) }
        if (disableTelemetry) add("--disable-telemetry")
        addAll(acpExtraArgs())
    }

    /** ACP flags without a dedicated daemon field. */
    fun acpExtraArgs(): List<String> = buildList {
        if (!includeDefaultCustomizations) add("--include-default-customizations=false")
        addAll(splitArgs(acpExtra))
    }

    fun tuiPreview(): String = (listOf("agy") + (if (skipPermissions) listOf("--dangerously-skip-permissions") else emptyList()) + tuiArgs().map(::shellQuote)).joinToString(" ")

    fun acpPreview(): String = (listOf("agy", "acp") + acpArgs().map(::shellQuote)).joinToString(" ") +
        listOfNotNull(model.takeIf { it.isNotBlank() }?.let { "model=$it" }, effort.takeIf { it.isNotBlank() }?.let { "effort=$it" },
            mode.takeIf { it.isNotBlank() }?.let { "mode=$it" }).joinToString(", ").let { if (it.isEmpty()) "" else "  ($it)" }

    /** JSON for the daemon's session.create / session.open "flags". */
    fun daemonJson(): kotlinx.serialization.json.JsonObject = kotlinx.serialization.json.buildJsonObject {
        put("skipPermissions", kotlinx.serialization.json.JsonPrimitive(skipPermissions))
        put("sandbox", kotlinx.serialization.json.JsonPrimitive(sandbox))
        put("disableTelemetry", kotlinx.serialization.json.JsonPrimitive(disableTelemetry))
        if (agent.isNotBlank()) put("agentName", kotlinx.serialization.json.JsonPrimitive(agent.trim()))
        if (model.isNotBlank()) put("model", kotlinx.serialization.json.JsonPrimitive(model.trim()))
        if (mode.isNotBlank()) put("mode", kotlinx.serialization.json.JsonPrimitive(mode.trim()))
        if (effort.isNotBlank()) put("thought", kotlinx.serialization.json.JsonPrimitive(effort.trim()))
        if (addDirs.isNotEmpty()) put("addDirs", kotlinx.serialization.json.JsonArray(addDirs.map { kotlinx.serialization.json.JsonPrimitive(it) }))
        val extra = acpExtraArgs()
        if (extra.isNotEmpty()) put("extraArgs", kotlinx.serialization.json.JsonArray(extra.map { kotlinx.serialization.json.JsonPrimitive(it) }))
    }
}

/**
 * Claude Code launch flags chosen on the phone. Saved in app settings and
 * applied to every new Claude chat session (`claude -p …` behind the daemon),
 * Claude terminal (`claude …`) and adopted conversation.
 */
@Serializable
data class ClaudeFlags(
    val skipPermissions: Boolean = true,
    val model: String = "",
    val effort: String = "",
    val mode: String = "",
    val agent: String = "",
    val addDirs: List<String> = emptyList(),
    val allowedTools: String = "",
    val disallowedTools: String = "",
    val appendSystemPrompt: String = "",
    val fallbackModel: String = "",
    val maxBudgetUsd: String = "",
    val mcpConfig: String = "",
    val strictMcpConfig: Boolean = false,
    val settingsJson: String = "",
    val extra: String = "",
    // Terminal (TUI) only
    val worktree: Boolean = false,
    val tuiExtra: String = "",
) {
    /** Flags shared by chat sessions and terminals (no model/mode/effort). */
    fun commonArgs(): List<String> = buildList {
        if (allowedTools.isNotBlank()) { add("--allowedTools"); add(allowedTools.trim()) }
        if (disallowedTools.isNotBlank()) { add("--disallowedTools"); add(disallowedTools.trim()) }
        if (appendSystemPrompt.isNotBlank()) { add("--append-system-prompt"); add(appendSystemPrompt.trim()) }
        if (fallbackModel.isNotBlank()) { add("--fallback-model"); add(fallbackModel.trim()) }
        if (mcpConfig.isNotBlank()) { add("--mcp-config"); add(mcpConfig.trim()) }
        if (strictMcpConfig) add("--strict-mcp-config")
        if (settingsJson.isNotBlank()) { add("--settings"); add(settingsJson.trim()) }
    }

    /** Extra flags for chat sessions (the daemon adds the protocol and session flags). */
    fun headlessExtraArgs(): List<String> = buildList {
        addAll(commonArgs())
        maxBudgetUsd.trim().toDoubleOrNull()?.takeIf { it > 0 }?.let { add("--max-budget-usd"); add(maxBudgetUsd.trim()) }
        addAll(splitArgs(extra))
    }

    /** Flags for `claude` in a terminal (skip-permissions is sent separately). */
    fun tuiArgs(): List<String> = buildList {
        if (model.isNotBlank()) { add("--model"); add(model.trim()) }
        if (effort.isNotBlank()) { add("--effort"); add(effort.trim()) }
        if (mode.isNotBlank() && mode != "default") { add("--permission-mode"); add(mode.trim()) }
        if (agent.isNotBlank()) { add("--agent"); add(agent.trim()) }
        addDirs.filter { it.isNotBlank() }.forEach { add("--add-dir"); add(it) }
        addAll(commonArgs())
        if (worktree) add("--worktree")
        addAll(splitArgs(tuiExtra))
    }

    /** Terminal flags for continuing an existing conversation (process options only). */
    fun tuiResumeArgs(): List<String> = copy(model = "", effort = "", mode = "", agent = "", worktree = false).tuiArgs()

    fun tuiPreview(): String = (listOf("claude") + (if (skipPermissions && (mode.isBlank() || mode == "default")) listOf("--dangerously-skip-permissions") else emptyList()) +
        tuiArgs().map(::shellQuote)).joinToString(" ")

    fun headlessPreview(): String {
        val parts = mutableListOf("claude", "-p")
        if (model.isNotBlank()) parts += listOf("--model", model.trim())
        if (effort.isNotBlank()) parts += listOf("--effort", effort.trim())
        if (mode.isNotBlank() && mode != "default") parts += listOf("--permission-mode", mode.trim())
        else if (skipPermissions) parts += "--dangerously-skip-permissions"
        if (agent.isNotBlank()) parts += listOf("--agent", agent.trim())
        addDirs.filter { it.isNotBlank() }.forEach { parts += listOf("--add-dir", it) }
        parts += headlessExtraArgs()
        return parts.joinToString(" ") { if (it == "claude" || it == "-p") it else shellQuote(it) }
    }

    /** JSON for the daemon's session.create / session.open "flags". */
    fun daemonJson(): kotlinx.serialization.json.JsonObject = kotlinx.serialization.json.buildJsonObject {
        put("skipPermissions", kotlinx.serialization.json.JsonPrimitive(skipPermissions))
        if (agent.isNotBlank()) put("agentName", kotlinx.serialization.json.JsonPrimitive(agent.trim()))
        if (model.isNotBlank()) put("model", kotlinx.serialization.json.JsonPrimitive(model.trim()))
        if (mode.isNotBlank()) put("mode", kotlinx.serialization.json.JsonPrimitive(mode.trim()))
        if (effort.isNotBlank()) put("thought", kotlinx.serialization.json.JsonPrimitive(effort.trim()))
        if (addDirs.isNotEmpty()) put("addDirs", kotlinx.serialization.json.JsonArray(addDirs.map { kotlinx.serialization.json.JsonPrimitive(it) }))
        val extra = headlessExtraArgs()
        if (extra.isNotEmpty()) put("extraArgs", kotlinx.serialization.json.JsonArray(extra.map { kotlinx.serialization.json.JsonPrimitive(it) }))
    }
}

private val argRe = Regex("""[^\s"']+|"((?:[^"\\]|\\.)*)"|'([^']*)'""")

/** Splits a shell-like argument string (supports '…' and "…" quoting). */
fun splitArgs(s: String): List<String> = argRe.findAll(s.trim()).map { m ->
    when {
        m.value.startsWith("\"") -> m.groupValues[1].replace("\\\"", "\"")
        m.value.startsWith("'") -> m.groupValues[2]
        else -> m.value
    }
}.toList()

fun shellQuote(a: String): String = if (a.isNotEmpty() && a.all { it.isLetterOrDigit() || it in "-_./=:@,+%~" }) a else "'" + a.replace("'", "'\\''") + "'"

/** A side question (/btw) about a Claude conversation: answered from it without becoming part of it. */
@Serializable
data class Aside(
    val id: String,
    val question: String = "",
    val answer: String = "",
    val error: String = "",
    val state: String = "pending", // pending|done|failed
    val ts: Long = 0,
    val doneTs: Long = 0,
    val followUp: String = "",
)
