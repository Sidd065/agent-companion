package io.github.sidd065.agentcompanion.core

import kotlinx.serialization.Serializable

// Protocol v2 models matching daemon wire structs.

@Serializable
data class ScriptDef(
    val id: String = "",
    val name: String = "",
    val command: String = "",
    val cwd: String? = null,
    val icon: String? = null,
    val runOnWorktreeCreate: Boolean = false,
    val runOnSettle: Boolean = false,
    val stopOnSettle: Boolean = false,
    val keybinding: String? = null,
)

@Serializable
data class Project(
    val id: String,
    val name: String = "",
    val path: String = "",
    val trackedFolders: List<String> = emptyList(),
    val icon: String? = null,
    val color: String? = null,
    val pinned: Boolean = false,
    val defaultAgent: String? = null,
    val defaultModel: String? = null,
    val defaultMode: String? = null,
    val defaultCheckout: String? = null,
    val defaultBranch: String? = null,
    val scripts: List<ScriptDef> = emptyList(),
    val isGit: Boolean = false,
    val createdAt: Long = 0,
    val updatedAt: Long = 0,
)

@Serializable
data class CheckpointFileStat(
    val path: String = "",
    val status: String = "",
    val additions: Int = 0,
    val deletions: Int = 0,
)

@Serializable
data class TurnCheckpoint(
    val turnIndex: Int = 0,
    val preRef: String? = null,
    val postRef: String? = null,
    val preSha: String? = null,
    val postSha: String? = null,
    val files: List<CheckpointFileStat> = emptyList(),
    val totalAdd: Int = 0,
    val totalDel: Int = 0,
    val skippedLarge: Int = 0,
    val createdAt: Long = 0,
    val error: String? = null,
)

@Serializable
data class GitFileStatus(
    val path: String = "",
    val origPath: String? = null,
    val staged: String? = null,
    val unstaged: String? = null,
    val untracked: Boolean = false,
    val conflicted: Boolean = false,
    val additions: Int = 0,
    val deletions: Int = 0,
)

@Serializable
data class GitStatus(
    val isRepo: Boolean = false,
    val root: String? = null,
    val branch: String? = null,
    val upstream: String? = null,
    val ahead: Int = 0,
    val behind: Int = 0,
    val detached: Boolean = false,
    val defaultBranch: String? = null,
    val isDefaultBranch: Boolean = false,
    val mergeInProgress: Boolean = false,
    val rebaseInProgress: Boolean = false,
    val stashCount: Int = 0,
    val files: List<GitFileStatus> = emptyList(),
    val totalAdd: Int = 0,
    val totalDel: Int = 0,
    val remoteUrl: String? = null,
    val forge: String? = null,
    val repoSlug: String? = null,
    val checkedAt: Long = 0,
)

@Serializable
data class GitBranch(
    val name: String = "",
    val remote: Boolean = false,
    val current: Boolean = false,
    val upstream: String? = null,
    val sha: String? = null,
    val subject: String? = null,
    val worktreePath: String? = null,
    val updatedAt: Long = 0,
)

@Serializable
data class WorktreeInfo(
    val path: String = "",
    val branch: String? = null,
    val head: String? = null,
    val isMain: Boolean = false,
    val isDetached: Boolean = false,
    val prunable: Boolean = false,
)

@Serializable
data class ScopedFileDiff(
    val path: String = "",
    val oldPath: String? = null,
    val status: String = "",
    val additions: Int = 0,
    val deletions: Int = 0,
    val binary: Boolean = false,
    val patch: String = "",
)

@Serializable
data class ScopedDiffResult(
    val scope: String = "",
    val baseRef: String? = null,
    val headRef: String? = null,
    val files: List<ScopedFileDiff> = emptyList(),
    val totalAdd: Int = 0,
    val totalDel: Int = 0,
    val truncated: Boolean = false,
    val rawDiff: String? = null,
)

@Serializable
data class PullRequestSummary(
    val number: Int = 0,
    val title: String = "",
    val body: String? = null,
    val state: String = "",
    val isDraft: Boolean = false,
    val url: String = "",
    val author: String = "",
    val headRefName: String = "",
    val baseRefName: String = "",
    val reviewDecision: String? = null,
    val additions: Int = 0,
    val deletions: Int = 0,
    val changedFiles: Int = 0,
    val updatedAt: String = "",
)

@Serializable
data class UsageBucket(
    val key: String = "",
    val turns: Int = 0,
    val inputTokens: Long = 0,
    val outputTokens: Long = 0,
    val cachedTokens: Long = 0,
    val totalTokens: Long = 0,
    val costUsd: Double = 0.0,
)

@Serializable
data class UsageSummary(
    val days: Int = 30,
    val turns: Int = 0,
    val inputTokens: Long = 0,
    val outputTokens: Long = 0,
    val cachedTokens: Long = 0,
    val totalTokens: Long = 0,
    val costUsd: Double = 0.0,
    val byDay: List<UsageBucket> = emptyList(),
    val byAgent: List<UsageBucket> = emptyList(),
    val byModel: List<UsageBucket> = emptyList(),
    val byProject: List<UsageBucket> = emptyList(),
)

@Serializable
data class ScheduledTask(
    val id: String = "",
    val name: String = "",
    val enabled: Boolean = true,
    val kind: String = "cron",
    val cron: String? = null,
    val intervalSec: Long = 0,
    val runAt: Long = 0,
    val projectId: String? = null,
    val cwd: String = "",
    val agent: String = "",
    val model: String? = null,
    val mode: String? = null,
    val prompt: String = "",
    val useWorktree: Boolean = false,
    val nextRunAt: Long = 0,
    val lastRunAt: Long = 0,
    val lastStatus: String? = null,
    val lastSessionId: String? = null,
    val lastError: String? = null,
)

@Serializable
data class ScheduleRunRecord(
    val id: String = "",
    val taskId: String = "",
    val taskName: String = "",
    val sessionId: String? = null,
    val status: String = "",
    val startedAt: Long = 0,
    val finishedAt: Long = 0,
    val error: String? = null,
)

@Serializable
data class SearchHit(
    val kind: String = "",
    val sessionId: String? = null,
    val projectId: String? = null,
    val title: String = "",
    val snippet: String? = null,
    val path: String? = null,
    val updatedAt: Long = 0,
)

@Serializable
data class ManagedProcess(
    val sessionId: String = "",
    val title: String = "",
    val agent: String = "",
    val state: String = "",
    val pid: Int = 0,
    val rssKiB: Long = 0,
)

@Serializable
data class HostHealth(
    val memTotalKiB: Long = 0,
    val memAvailableKiB: Long = 0,
    val memUsedPct: Double = 0.0,
    val psiSomeAvg10: Double = 0.0,
    val psiFullAvg10: Double = 0.0,
    val loadAvg1: Double = 0.0,
    val loadAvg5: Double = 0.0,
    val processes: List<ManagedProcess> = emptyList(),
    val checkedAt: Long = 0,
)

@Serializable
data class CustomModel(
    val slug: String,
    val name: String = "",
)

@Serializable
data class ProviderModelInfo(
    val modelId: String,
    val name: String = "",
    val description: String = "",
    val provider: String = "",
    val contextWindow: Int = 0,
    val supportsReasoning: Boolean = false,
    val supportsTools: Boolean = false,
    val supportsImages: Boolean = false,
    val isCustom: Boolean = false,
)

@Serializable
data class ProviderModeInfo(
    val id: String,
    val name: String = "",
    val description: String = "",
)

@Serializable
data class ProviderModelsResult(
    val provider: String = "codex",
    val defaultModelId: String = "",
    val openRouterConfigured: Boolean = false,
    val nativeAuthConfigured: Boolean = false,
    val models: List<ProviderModelInfo> = emptyList(),
    val customModels: List<CustomModel> = emptyList(),
    val availableModes: List<ProviderModeInfo> = emptyList(),
    val reasoningEfforts: List<String> = emptyList(),
)

@Serializable
data class ProviderConfig(
    val id: String,
    val name: String = "",
    val driver: String = "",
    val command: String = "",
    val args: List<String> = emptyList(),
    val env: Map<String, String>? = null,
    val enabled: Boolean = true,
    val defaultModel: String = "",
    val customModels: List<CustomModel> = emptyList(),
    val icon: String = "",
    val builtin: Boolean = false,
    val features: List<String> = emptyList(),
)

@Serializable
data class ProvidersListResult(
    val agents: List<AgentProfile> = emptyList(),
    val configs: List<ProviderConfig> = emptyList(),
)

@Serializable
data class ProviderHealth(
    val id: String,
    val available: Boolean = false,
    val command: String = "",
    val version: String = "",
    val authStatus: String = "",
    val message: String = "",
    val latencyMs: Long = 0,
    val checkedAt: Long = 0,
)
