package io.github.sidd065.agentcompanion.ui

import android.app.Application
import android.graphics.Bitmap
import java.awt.image.BufferedImage
import javax.imageio.ImageIO
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.EditNote
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.compose.ComposeNavigator
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.test.core.app.ApplicationProvider
import io.github.sidd065.agentcompanion.App
import io.github.sidd065.agentcompanion.core.AgentProfile
import io.github.sidd065.agentcompanion.core.HostConfig
import io.github.sidd065.agentcompanion.core.Item
import io.github.sidd065.agentcompanion.core.LiveArtifact
import io.github.sidd065.agentcompanion.core.LiveMessage
import io.github.sidd065.agentcompanion.core.LiveState
import io.github.sidd065.agentcompanion.core.LiveSubagent
import io.github.sidd065.agentcompanion.core.LiveTask
import io.github.sidd065.agentcompanion.core.Perm
import io.github.sidd065.agentcompanion.core.PermOption
import io.github.sidd065.agentcompanion.core.SessionNote
import io.github.sidd065.agentcompanion.core.Tool
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.net.ConnState
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.Noise
import io.github.sidd065.agentcompanion.net.SecureSocket
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.chat.ActivitySheet
import io.github.sidd065.agentcompanion.ui.chat.ArtifactScreen
import io.github.sidd065.agentcompanion.ui.chat.ChatScreen
import io.github.sidd065.agentcompanion.ui.chat.Composer
import io.github.sidd065.agentcompanion.ui.chat.ComposerState
import io.github.sidd065.agentcompanion.ui.chat.PlanReviewCard
import io.github.sidd065.agentcompanion.ui.chat.QuestionCard
import io.github.sidd065.agentcompanion.ui.chat.SessionNotesSheet
import io.github.sidd065.agentcompanion.ui.chat.TranscriptActions
import io.github.sidd065.agentcompanion.ui.chat.TranscriptRow
import io.github.sidd065.agentcompanion.ui.chat.buildRows
import io.github.sidd065.agentcompanion.ui.files.FilesScreen
import io.github.sidd065.agentcompanion.ui.manage.ManageScreen
import io.github.sidd065.agentcompanion.ui.sessions.DiffScreen
import io.github.sidd065.agentcompanion.ui.sessions.SessionsScreen
import io.github.sidd065.agentcompanion.ui.settings.SettingsScreen
import io.github.sidd065.agentcompanion.ui.theme.AppTheme
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.OkHttpClient
import org.junit.AfterClass
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.BeforeClass
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File
import java.io.FileOutputStream
import java.net.ServerSocket
import java.net.URI
import java.net.URLDecoder
import java.util.Base64
import java.util.UUID
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit

@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [34], qualifiers = "w360dp-h800dp-xxhdpi")
class VisualAuditScreenshotTest {

    @get:Rule
    val composeRule = createComposeRule()

    data class LiveDaemonFixture(
        val label: String,
        val homeDir: File,
        val stDir: File,
        val rtDir: File,
        val process: Process,
        val config: HostConfig,
        val deviceKey: Noise.KeyPair,
    )

    companion object {
        private var rootTmp: File? = null
        private var http: OkHttpClient? = null
        private var hostRpi: LiveDaemonFixture? = null
        private var hostDevBox: LiveDaemonFixture? = null
        private lateinit var repoDir: File
        private lateinit var gate1Path: String
        private const val SEEDED_SESSION_ID = "audit-session-gate1"

        private val outDir: File
            get() = File(System.getenv("VISUAL_ANDROID_OUT") ?: "/tmp/visual-android").apply { mkdirs() }

        @JvmStatic
        @BeforeClass
        fun setUpDaemons() {
            val userHome = System.getProperty("user.home")!!
            val tmp = File(userHome, ".cache/agc-vis-" + UUID.randomUUID().toString().take(6)).apply { mkdirs() }
            rootTmp = tmp

            val startDir = File(System.getProperty("user.dir")!!).canonicalFile
            val repoRoot = generateSequence(startDir) { it.parentFile }
                .firstOrNull { File(it, "daemon/go.mod").isFile }
                ?: error("Could not locate daemon/go.mod")
            val daemonDir = File(repoRoot, "daemon")
            val goBin = listOf(
                File(userHome, "tools/go-dl/go/bin/go"),
                File(userHome, ".local/go/bin/go"),
                File("/usr/local/go/bin/go"),
                File("/usr/bin/go"),
            ).firstOrNull { it.canExecute() } ?: error("Go not found")
            val agentcBin = File(tmp, "agentc")
            val build = ProcessBuilder(goBin.absolutePath, "build", "-o", agentcBin.absolutePath, ".")
                .directory(daemonDir)
                .redirectErrorStream(true)
                .start()
            val buildOut = build.inputStream.bufferedReader().readText()
            assertEquals("go build failed: $buildOut", 0, build.waitFor())

            val client = OkHttpClient.Builder()
                .connectTimeout(10, TimeUnit.SECONDS)
                .readTimeout(30, TimeUnit.SECONDS)
                .build()
            http = client

            hostRpi = startAndPairDaemon(agentcBin, File(tmp, "rpi"), "rpi", client, seedData = true)
            hostDevBox = startAndPairDaemon(agentcBin, File(tmp, "dev"), "dev-box", client, seedData = false)
        }

        private fun writeSamplePng(dest: File, w: Int, h: Int) {
            dest.parentFile?.mkdirs()
            val img = BufferedImage(w, h, BufferedImage.TYPE_INT_ARGB)
            for (y in 0 until h) {
                for (x in 0 until w) {
                    val r = (40 + (180 * x) / w).coerceIn(0, 255)
                    val g = (80 + (140 * y) / h).coerceIn(0, 255)
                    val b = 210
                    val argb = (0xFF shl 24) or (r shl 16) or (g shl 8) or b
                    img.setRGB(x, y, argb)
                }
            }
            ImageIO.write(img, "png", dest)
        }

        private fun startAndPairDaemon(
            agentcBin: File,
            baseDir: File,
            name: String,
            client: OkHttpClient,
            seedData: Boolean,
        ): LiveDaemonFixture {
            val homeDir = File(baseDir, "home").apply { mkdirs() }
            val cfgDir = File(baseDir, "cfg").apply { mkdirs() }
            val cfgAgentc = File(cfgDir, "agentc").apply { mkdirs() }
            val stDir = File(baseDir, "st").apply { mkdirs() }
            val dtDir = File(baseDir, "dt").apply { mkdirs() }
            val rtDir = File(baseDir, "rt").apply { mkdirs() }

            if (seedData) {
                val repo = File(homeDir, "webmirror").apply { mkdirs() }
                repoDir = repo
                File(repo, "README.md").writeText("# Webmirror\n\n![Diagram](./arch.png)\n")
                writeSamplePng(File(repo, "arch.png"), 360, 200)
                val spaceImg = File(repo, "storyboard 2k.png")
                writeSamplePng(spaceImg, 480, 270)

                ProcessBuilder("git", "init", "-b", "main").directory(repo).start().waitFor()
                ProcessBuilder("git", "config", "user.name", "Bot").directory(repo).start().waitFor()
                ProcessBuilder("git", "config", "user.email", "b@local").directory(repo).start().waitFor()
                ProcessBuilder("git", "add", ".").directory(repo).start().waitFor()
                ProcessBuilder("git", "commit", "-m", "init").directory(repo).start().waitFor()
                File(repo, "README.md").writeText("# Webmirror v2\n\nUpdated pipeline docs.\n")

                // Seed session + gate1_review.md artifact
                val sessDir = File(stDir, "agentc/sessions").apply { mkdirs() }
                val now = System.currentTimeMillis()
                File(sessDir, "$SEEDED_SESSION_ID.json").writeText(
                    """
                    {
                      "meta": {
                        "id": "$SEEDED_SESSION_ID",
                        "agent": "agy",
                        "cwd": "${repo.absolutePath}",
                        "branch": "main",
                        "title": "Take Over Claude Code Session & Gate 1 Review",
                        "createdAt": ${now - 120000},
                        "updatedAt": $now,
                        "skipPerms": true
                      },
                      "seq": 3,
                      "items": [
                        {"id": "u1", "kind": "user", "text": "Review the duckvideo pipeline", "ts": ${now - 60000}},
                        {"id": "a1", "kind": "agent", "text": "Gate 1 architecture is ready for review.", "ts": ${now - 30000}}
                      ]
                    }
                    """.trimIndent(),
                )

                val brainDir = File(homeDir, ".gemini/antigravity-cli/brain/$SEEDED_SESSION_ID").apply { mkdirs() }
                val artFile = File(brainDir, "gate1_review.md")
                gate1Path = artFile.absolutePath
                artFile.writeText(
                    """
                    > [!IMPORTANT]
                    > **GPU Budget & Hygiene Status**: Vast.ai Blender instance `62509883` (`duckvideo-blender`, RTX 3060 @ `${'$'}0.0816/hr`) was spun up exclusively for headless Blender 4.2 OptiX rendering and **immediately destroyed** (`${'$'}0.04` spent out of `${'$'}100.00` budget).

                    ## 1. End-to-End 3D-Guided Hub-and-Spoke Architecture

                    ```mermaid
                    flowchart LR
                      Ref["ref.mp4 (360x640)"] --> CleanRefs["Curated Clean Frames"]
                      CleanRefs --> Anchor16["Master Anchor 16:9 (2560x1440)"]
                      CleanRefs --> Anchor9["Master Anchor 9:16 (1440x2560)"]
                      Anchor16 & Anchor9 --> Calib["3D Pinhole Calibration (Z=0 Pond)"]
                      Calib --> Blender["Headless Blender 4.2 (362f @ 24fps)"]
                      Blender --> Mod01["Modules 01..12 (RGB + Depth)"]
                      Anchor16 & Mod01 --> H3["MiniMax H3 ControlNet"]
                      H3 --> QA["qa_check.py (Seam SSIM + Drift)"]
                    ```

                    | Module | Resolution | Frame Grid | QA Metrics & Notes |
                    |---|---|---|---|
                    | `01_dive` | `2560x1440` + `1440x2560` | `362f @ 24fps` | Seam SSIM `0.994`, zero corner flow |
                    | `02_huddle` | `2560x1440` + `1440x2560` | `362f @ 24fps` | Pairwise duckling repulsion verified |

                    ````carousel
                    ![Storyboard 2K Render](file://${repo.absolutePath}/storyboard%202k.png)
                    <!-- slide -->
                    ![Missing Render Asset](/nonexistent/missing_render.png)
                    <!-- slide -->
                    ```python
                    def validate_minimax_grid(frames: int) -> bool:
                        return (frames - 5) % 17 == 0
                    ```
                    ````
                    """.trimIndent(),
                )
                File(brainDir, "gate1_review.md.metadata.json").writeText(
                    """{"summary":"Gate 1 Review & Engineering Architecture","requestFeedback":true,"userFacing":true,"updatedAt":"2099-01-01T00:00:00Z"}""",
                )
            }

            val port = ServerSocket(0).use { it.localPort }
            val listenUrl = "http://127.0.0.1:$port"
            File(cfgAgentc, "config.json").writeText(
                "{\"tunnel\":\"none\",\"listen\":\"127.0.0.1:$port\",\"publicUrl\":\"$listenUrl\"}",
            )
            val envMap = mapOf(
                "HOME" to homeDir.absolutePath,
                "XDG_CONFIG_HOME" to cfgDir.absolutePath,
                "XDG_STATE_HOME" to stDir.absolutePath,
                "XDG_DATA_HOME" to dtDir.absolutePath,
                "XDG_RUNTIME_DIR" to rtDir.absolutePath,
            )
            val proc = ProcessBuilder(agentcBin.absolutePath, "serve", "--listen", "127.0.0.1:$port")
                .apply { environment().putAll(envMap) }
                .redirectErrorStream(true)
                .start()

            val sock = File(rtDir, "agentc/agentc.sock")
            val deadline = System.currentTimeMillis() + 10_000
            while (!sock.exists() && System.currentTimeMillis() < deadline) Thread.sleep(50)
            assertTrue("Daemon socket missing for $name", sock.exists())

            val pairProc = ProcessBuilder(agentcBin.absolutePath, "pair", "--url", listenUrl)
                .apply { environment().putAll(envMap) }
                .redirectErrorStream(true)
                .start()
            val pairOut = pairProc.inputStream.bufferedReader().readText()
            assertEquals(0, pairProc.waitFor())
            val pairUrl = Regex("http://127\\.0\\.0\\.1:\\d+/p/\\S+").find(pairOut)!!.value
            val uri = URI(pairUrl)
            val baseUrl = "${uri.scheme}://${uri.authority}"
            val pairId = uri.path.substringAfter("/p/")
            val frag = uri.rawFragment.split("&").associate {
                val kv = it.split("=", limit = 2)
                kv[0] to URLDecoder.decode(kv.getOrElse(1) { "" }, "UTF-8")
            }
            val devKey = Noise.generateKeyPair()
            val hostKeyB64 = frag["k"]!!
            val daemonPub = Base64.getUrlDecoder().decode(hostKeyB64)
            val psk = Base64.getUrlDecoder().decode(frag["s"]!!)

            val pairedFuture = CompletableFuture<JsonObject>()
            val pairSock = SecureSocket(
                http = client,
                baseUrl = baseUrl,
                daemonKey = daemonPub,
                deviceKey = devKey,
                hello = buildJsonObject {
                    put("v", 1); put("purpose", "rpc"); put("name", "AuditPhone"); put("platform", "android"); put("app", "0.6.0")
                },
                listener = object : SecureSocket.Listener {
                    override fun onOpen(socket: SecureSocket, reply: JsonObject) { pairedFuture.complete(reply) }
                    override fun onClosed(error: String?) {
                        if (!pairedFuture.isDone) pairedFuture.completeExceptionally(RuntimeException(error ?: "closed"))
                    }
                },
                mode = SecureSocket.MODE_PAIR,
                pairId = pairId,
                psk = psk,
            )
            pairSock.connect()
            val reply = pairedFuture.get(10, TimeUnit.SECONDS)
            pairSock.close()

            val hostId = reply["hostId"]?.jsonPrimitive?.content ?: "host-$name"
            val cfg = HostConfig(
                id = hostId,
                name = name,
                url = baseUrl,
                hostKey = hostKeyB64,
                devicePub = Base64.getEncoder().encodeToString(devKey.publicKey),
                devicePrivWrapped = "test-wrapped-$name",
                deviceId = reply["deviceId"]?.jsonPrimitive?.content.orEmpty(),
            )
            return LiveDaemonFixture(name, homeDir, stDir, rtDir, proc, cfg, devKey)
        }

        @JvmStatic
        @AfterClass
        fun tearDownDaemons() {
            listOf(hostRpi, hostDevBox).forEach { f ->
                if (f != null) {
                    runCatching {
                        f.process.destroy()
                        if (!f.process.waitFor(2, TimeUnit.SECONDS)) f.process.destroyForcibly()
                    }
                }
            }
            http?.dispatcher?.executorService?.shutdown()
            rootTmp?.deleteRecursively()
        }
    }

    private fun saveShot(name: String) {
        composeRule.waitForIdle()
        val bmp = composeRule.onRoot().captureToImage().asAndroidBitmap()
        val file = File(outDir, name)
        FileOutputStream(file).use { bmp.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private fun connectBothHosts(): Pair<HostConnection, HostConnection> = runBlocking {
        val rpi = HostConnection(
            config = hostRpi!!.config,
            http = http!!,
            deviceName = "AuditPhone",
            onNotifySeq = { _, _ -> },
            lastNotifSeq = { 0L },
            keyPairProvider = { hostRpi!!.deviceKey },
        )
        val dev = HostConnection(
            config = hostDevBox!!.config,
            http = http!!,
            deviceName = "AuditPhone",
            onNotifySeq = { _, _ -> },
            lastNotifSeq = { 0L },
            keyPairProvider = { hostDevBox!!.deviceKey },
        )
        rpi.start()
        dev.start()
        withTimeout(10_000) {
            rpi.rpc.state.first { it is ConnState.Connected }
            dev.rpc.state.first { it is ConnState.Connected }
        }
        rpi.refreshSessions(force = true)
        dev.refreshSessions(force = true)

        val app = ApplicationProvider.getApplicationContext<App>()
        // Inject test connections into AppGraph via prefs + reflection so activeConnection() & Coil RemoteImage work!
        app.graph.prefs.update {
            it.copy(hosts = listOf(hostRpi!!.config, hostDevBox!!.config), activeHostId = hostRpi!!.config.id)
        }
        val connsField = app.graph.javaClass.getDeclaredField("_connections").apply { isAccessible = true }
        @Suppress("UNCHECKED_CAST")
        val stateFlow = connsField.get(app.graph) as kotlinx.coroutines.flow.MutableStateFlow<List<HostConnection>>
        stateFlow.value = listOf(rpi, dev)
        CurrentHost.id = hostRpi!!.config.id
        ExplicitHostChoice.hostId = hostRpi!!.config.id
        rpi to dev
    }

    @Composable
    private fun AuditHarness(theme: String = "dark", textScale: Float = 1f, content: @Composable () -> Unit) {
        val nav = rememberNavController()
        // Attach a minimal graph so popBackStack / navigate calls don't crash
        NavHost(navController = nav, startDestination = "audit_root") {
            composable("audit_root") {
                val snack = remember { SnackbarHostState() }
                AppTheme(theme = theme, dynamic = false, textScale = textScale) {
                    CompositionLocalProvider(LocalNav provides nav, LocalSnack provides snack) {
                        Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                            content()
                        }
                    }
                }
            }
        }
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @Test
    fun render01_chatTranscriptPermAndSkipPerms_360_dark() {
        val (rpi, _) = connectBothHosts()
        try {
            val deepPath = "../.claude/projects/-home-siddharth-webmirror/memory/no-force-push-add-commit.md"
            val sampleItems = listOf(
                Item(id = "t1", kind = "tool", tool = Tool(callId = "c1", name = "Read", kind = "read", title = "Read output.txt", status = "completed", input = buildJsonObject { put("file_path", "/home/siddharth/webmirror/output.txt") })),
                Item(id = "t2", kind = "tool", tool = Tool(callId = "c2", name = "Read", kind = "read", title = "Read webmirror-project.md", status = "completed", input = buildJsonObject { put("file_path", "/home/siddharth/webmirror/webmirror-project.md") })),
                Item(id = "p1", kind = "perm", state = "answered", perm = Perm(reqId = "r1", callId = "c2", kind = "tool", title = "File $deepPath", chosen = "allow_once", options = listOf(PermOption("allow_once", "Allow once", "allow_once")))),
                Item(id = "t3", kind = "tool", tool = Tool(callId = "c3", name = "Read", kind = "read", title = "Read MEMORY.md", status = "completed", input = buildJsonObject { put("file_path", "/home/siddharth/webmirror/MEMORY.md") })),
                Item(id = "p2", kind = "perm", state = "answered", perm = Perm(reqId = "r2", callId = "c3", kind = "tool", title = "File $deepPath", chosen = "allow_once", options = listOf(PermOption("allow_once", "Allow once", "allow_once")))),
                Item(id = "t4", kind = "tool", tool = Tool(callId = "c4", name = "Read", kind = "read", title = "Read real-world-testing.md", status = "completed", input = buildJsonObject { put("file_path", "/home/siddharth/webmirror/real-world-testing.md") })),
                Item(id = "p3", kind = "perm", state = "answered", perm = Perm(reqId = "r3", callId = "c4", kind = "tool", title = "File $deepPath", chosen = "allow_once", options = listOf(PermOption("allow_once", "Allow once", "allow_once")))),
                Item(id = "t5", kind = "tool", tool = Tool(callId = "c5", name = "Read", kind = "read", title = "Reading no-force-push-add-commit.md", status = "in_progress", input = buildJsonObject { put("file_path", deepPath) })),
                Item(
                    id = "p4",
                    kind = "perm",
                    state = "pending",
                    perm = Perm(
                        reqId = "r4",
                        callId = "c5",
                        kind = "tool",
                        tool = "read_file",
                        title = "File $deepPath",
                        options = listOf(
                            PermOption("allow_once", "Allow once", "allow_once"),
                            PermOption("reject_once", "Deny", "reject_once"),
                        ),
                    ),
                ),
            )
            val rows = buildRows(sampleItems, showThoughts = true)
            val actions = TranscriptActions(
                conn = rpi,
                sessionId = SEEDED_SESSION_ID,
                onUserLong = {},
                onAgentLong = {},
                onSendText = {},
                onPrefill = {},
                onOpenFile = {},
                onQueueAction = { _, _ -> },
                onApprovePlan = {},
                agent = "agy",
            )

            composeRule.setContent {
                AuditHarness(theme = "dark") {
                    val ex = LocalExtra.current
                    val compState = remember { ComposerState() }
                    val focus = remember { FocusRequester() }
                    Scaffold(
                        topBar = {
                            TopAppBar(
                                navigationIcon = { IconButton(onClick = {}) { Icon(Icons.AutoMirrored.Outlined.ArrowBack, null) } },
                                title = {
                                    Column {
                                        Text("Take Over Claude Code Session", maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleMedium)
                                        Text("needs permission · ~/webmirror", maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall, color = ex.warning)
                                    }
                                },
                                actions = {
                                    IconButton(onClick = {}) { Icon(Icons.Outlined.EditNote, null) }
                                    IconButton(onClick = {}) { Icon(Icons.Outlined.Folder, null) }
                                    IconButton(onClick = {}) { Icon(Icons.Outlined.MoreVert, null) }
                                },
                            )
                        },
                        bottomBar = {
                            Composer(
                                agentName = "agy",
                                state = compState,
                                busy = true,
                                delivery = "queue",
                                enterSends = false,
                                modelLabel = "Gemini 4 Carbon (High)",
                                thoughtLabel = "High",
                                modeLabel = "Default",
                                skipPerms = true,
                                commands = emptyList(),
                                focus = focus,
                                onSend = { _, _, _ -> },
                                onStop = {},
                                onPickPhoto = {},
                                onPickFile = {},
                                onBrowseHost = {},
                                onModel = {},
                                onThinking = {},
                                onMode = {},
                                onLocalCommand = {},
                                complete = { emptyList() },
                            )
                        },
                    ) { pad ->
                        LazyColumn(Modifier.fillMaxSize().padding(pad)) {
                            itemsIndexed(rows) { i, r ->
                                TranscriptRow(r, actions, isLast = i == rows.lastIndex, activeId = "p4", wrap = false, cwd = "/home/siddharth/webmirror")
                            }
                        }
                    }
                }
            }
            saveShot("02-chat-perm-transcript-360-dark.png")
        } finally {
            rpi.close()
        }
    }

    @Test
    fun render02_questionAndLongPlanCards_360_dark() {
        val (rpi, _) = connectBothHosts()
        try {
            val qItem = Item(
                id = "q1",
                kind = "tool",
                tool = Tool(
                    callId = "cq1",
                    name = "ask_question",
                    kind = "other",
                    status = "in_progress",
                    input = buildJsonObject {
                        put(
                            "questions",
                            buildJsonArray {
                                add(
                                    buildJsonObject {
                                        put("question", "Which Cloudflare tunnel auth mode should we configure for jdlf.cc?")
                                        put("is_multi_select", false)
                                        put(
                                            "options",
                                            buildJsonArray {
                                                add(JsonPrimitive("(Recommended) Static named tunnel per host"))
                                                add(JsonPrimitive("Quick ephemeral trycloudflare tunnel"))
                                            },
                                        )
                                    },
                                )
                            },
                        )
                    },
                ),
            )
            val longPlanText = (1..32).joinToString("\n\n") { idx ->
                "### Step $idx: Architecture & Verification Gate\nVerify multi-host routing, Noise IKpsk1 handshake, and full responsive UI layout at 360dp."
            }
            val planItem = Item(
                id = "pl1",
                kind = "tool",
                tool = Tool(
                    callId = "cp1",
                    name = "write_file",
                    kind = "edit",
                    status = "completed",
                    input = buildJsonObject {
                        put("Path", "/home/siddharth/.gemini/brain/implementation_plan.md")
                        put("Content", longPlanText)
                        put(
                            "ArtifactMetadata",
                            buildJsonObject {
                                put("RequestFeedback", true)
                                put("Summary", "Complete Top-to-Bottom Audit Plan")
                            },
                        )
                    },
                ),
            )
            val actions = TranscriptActions(
                conn = rpi,
                sessionId = SEEDED_SESSION_ID,
                onUserLong = {},
                onAgentLong = {},
                onSendText = {},
                onPrefill = {},
                onOpenFile = {},
                onQueueAction = { _, _ -> },
                onApprovePlan = {},
                agent = "agy",
            )
            composeRule.setContent {
                AuditHarness(theme = "dark") {
                    Column(Modifier.fillMaxSize().padding(vertical = 8.dp)) {
                        QuestionCard(item = qItem, active = true, actions = actions)
                        PlanReviewCard(item = planItem, active = true, actions = actions)
                    }
                }
            }
            saveShot("03-question-and-plan-cards-360-dark.png")
        } finally {
            rpi.close()
        }
    }

    @Test
    fun render03_artifactGate1_darkAndLight_360() {
        val (rpi, dev) = connectBothHosts()
        try {
            var themeMode by mutableIntStateOf(0)
            composeRule.setContent {
                AuditHarness(theme = if (themeMode == 0) "dark" else "light") {
                    ArtifactScreen(sessionId = SEEDED_SESSION_ID, path = gate1Path)
                }
            }
            Thread.sleep(1000)
            saveShot("04-artifact-gate1-360-dark.png")

            composeRule.runOnIdle { themeMode = 1 }
            Thread.sleep(900)
            saveShot("05-artifact-gate1-360-light.png")
        } finally {
            rpi.close()
            dev.close()
        }
    }

    @Test
    fun render04_notesActivityDiffFilesManageSettings_360() {
        val (rpi, dev) = connectBothHosts()
        try {
            val live = LiveState(
                running = false,
                tasks = emptyList(),
                subagents = listOf(
                    LiveSubagent(id = "sub1", role = "T3 User Docs and Desktop Survey", type = "devbox-researcher", state = "COMPLETED", updated = System.currentTimeMillis() - 3600_000),
                    LiveSubagent(id = "sub2", role = "T3 Web Chat and Shell UI Survey", type = "devbox-researcher", state = "COMPLETED", updated = System.currentTimeMillis() - 3600_000),
                    LiveSubagent(id = "sub3", role = "OpenRouter Harness Integration Team", type = "teamwork", state = "COMPLETED", updated = System.currentTimeMillis() - 60_000),
                ),
                artifacts = listOf(
                    LiveArtifact(name = "gate1_review.md", path = gate1Path, kind = "markdown", requestFeedback = true, userFacing = true, updated = System.currentTimeMillis()),
                ),
                messages = (1..12).map { idx ->
                    LiveMessage(id = "m$idx", sender = "subagent", title = "Completed survey $idx", content = "All checks done", ts = System.currentTimeMillis() - idx * 60_000L)
                },
            )
            var step by mutableIntStateOf(0)
            composeRule.setContent {
                val scale = if (step == 5) 1.3f else 1f
                AuditHarness(theme = "dark", textScale = scale) {
                    when (step) {
                        0 -> SessionsScreen()
                        1 -> ChatScreen(sessionId = SEEDED_SESSION_ID, prefill = "")
                        2 -> FilesScreen(initialPath = repoDir.absolutePath)
                        3 -> ManageScreen()
                        4 -> DiffScreen(sessionId = SEEDED_SESSION_ID, path = repoDir.absolutePath)
                        5 -> SettingsScreen()
                        6 -> Box(Modifier.fillMaxSize()) {
                            SessionNotesSheet(
                                note = SessionNote(
                                    text = "- [ ] Configure static Cloudflare tunnel on jdlf.cc\n- [x] Verify read auto-allow\n- [ ] Fix UI polish across mobile & desktop",
                                    updatedAt = System.currentTimeMillis(),
                                ),
                                sessionTitle = "Gate 1 Review & Engineering Architecture",
                                onSave = {},
                                onInsertIntoComposer = {},
                                onDismiss = {},
                            )
                        }
                        7 -> Box(Modifier.fillMaxSize()) {
                            ActivitySheet(
                                conn = rpi,
                                sessionId = SEEDED_SESSION_ID,
                                live = live,
                                initialTab = "agents",
                                onDismiss = {},
                                onOpenArtifact = {},
                            )
                        }
                    }
                }
            }
            val names = listOf(
                0 to "01-sessions-screen-360-dark.png",
                1 to "02b-real-chat-screen-360-dark.png",
                2 to "08a-files-screen-2hosts-360-dark.png",
                3 to "08b-manage-screen-2hosts-360-dark.png",
                4 to "07b-diff-screen-tabs-360-dark.png",
                5 to "10-settings-360-font130.png",
                6 to "06-session-notes-sheet-360-dark.png",
                7 to "07a-activity-sheet-tabs-360-dark.png",
            )
            for ((idx, filename) in names) {
                composeRule.runOnIdle { step = idx }
                Thread.sleep(650)
                saveShot(filename)
            }
        } finally {
            rpi.close()
            dev.close()
        }
    }
}
