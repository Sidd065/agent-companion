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
) {
    val busy: Boolean get() = state == "running" || state == "starting" || state == "waiting"
    val needsInput: Boolean get() = !attention.isNullOrEmpty() || state == "waiting"
    val displayTitle: String get() = title.ifBlank { "Untitled conversation" }
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
data class AgyModel(val id: String, val name: String = "")

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
