package io.github.sidd065.agentcompanion.ui.files

import android.app.Application
import androidx.compose.runtime.saveable.SaverScope
import androidx.core.content.FileProvider
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.ComposeNavigator
import androidx.navigation.compose.composable
import androidx.navigation.createGraph
import androidx.navigation.navArgument
import androidx.test.core.app.ApplicationProvider
import io.github.sidd065.agentcompanion.App
import io.github.sidd065.agentcompanion.core.AgentProfile
import io.github.sidd065.agentcompanion.core.AppSettings
import io.github.sidd065.agentcompanion.core.CustomModel
import io.github.sidd065.agentcompanion.core.FsList
import io.github.sidd065.agentcompanion.core.HostConfig
import io.github.sidd065.agentcompanion.core.ProviderConfig
import io.github.sidd065.agentcompanion.net.ConnState
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.Noise
import io.github.sidd065.agentcompanion.net.SecureSocket
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.CurrentHost
import io.github.sidd065.agentcompanion.ui.ExplicitHostChoice
import io.github.sidd065.agentcompanion.ui.ROUTE_ARTIFACT
import io.github.sidd065.agentcompanion.ui.ROUTE_CHAT
import io.github.sidd065.agentcompanion.ui.ROUTE_DIFF
import io.github.sidd065.agentcompanion.ui.ROUTE_FILE
import io.github.sidd065.agentcompanion.ui.ROUTE_FOLDER
import io.github.sidd065.agentcompanion.ui.ROUTE_NEW
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.dec
import io.github.sidd065.agentcompanion.ui.manage.buildCodexProviderUpsertConfig
import io.github.sidd065.agentcompanion.ui.navString
import io.github.sidd065.agentcompanion.ui.resolveEffectiveHostId
import io.github.sidd065.agentcompanion.ui.resolveLocalFileUriPath
import io.github.sidd065.agentcompanion.ui.selectActiveHost
import io.github.sidd065.agentcompanion.ui.sessions.resolveNewSessionHostId
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.AfterClass
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.BeforeClass
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File
import java.net.ServerSocket
import java.net.URI
import java.net.URLDecoder
import java.util.Base64
import java.util.UUID
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MultiHostFilesAndRoutingTest {

    data class LiveDaemonFixture(
        val label: String,
        val homeDir: File,
        val rtDir: File,
        val process: Process,
        val config: HostConfig,
        val deviceKey: Noise.KeyPair,
    )

    companion object {
        private var rootTmp: File? = null
        private var http: OkHttpClient? = null
        private var hostAFixture: LiveDaemonFixture? = null
        private var hostBFixture: LiveDaemonFixture? = null

        @JvmStatic
        @BeforeClass
        fun setUpDaemons() {
            val userHome = System.getProperty("user.home")!!
            assertFalse("user.home must not be under /tmp", userHome.startsWith("/tmp"))
            val tmp = File(userHome, ".cache/agc-mh-" + UUID.randomUUID().toString().take(8))
            assertFalse("scratch must never be under /tmp", tmp.absolutePath.startsWith("/tmp"))
            assertTrue(
                "scratch path (${tmp.absolutePath.length} chars) must be < 70 chars for AF_UNIX agentc.sock",
                tmp.absolutePath.length < 70,
            )
            tmp.mkdirs()
            rootTmp = tmp

            val startDir = File(System.getProperty("user.dir")!!).canonicalFile
            val repoRoot = generateSequence(startDir) { it.parentFile }
                .firstOrNull { File(it, "daemon/go.mod").isFile }
                ?: error("Could not locate daemon/go.mod above $startDir")
            val daemonDir = File(repoRoot, "daemon")
            val pathCandidates = System.getenv("PATH").orEmpty()
                .split(File.pathSeparatorChar)
                .filter { it.isNotBlank() }
                .map { File(it, "go") }
            val goBin = (listOf(
                File(userHome, "tools/go-dl/go/bin/go"),
                File(userHome, ".local/go/bin/go"),
                File("/usr/local/go/bin/go"),
                File("/usr/bin/go"),
            ) + pathCandidates).firstOrNull { it.canExecute() } ?: error("Go toolchain not found")
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

            hostAFixture = startAndPairDaemon(agentcBin, File(tmp, "hostA"), "HostAlpha", client)
            hostBFixture = startAndPairDaemon(agentcBin, File(tmp, "hostB"), "HostBeta", client)
        }

        private fun startAndPairDaemon(
            agentcBin: File,
            baseDir: File,
            name: String,
            client: OkHttpClient,
        ): LiveDaemonFixture {
            val homeDir = File(baseDir, "home").apply { mkdirs() }
            val cfgDir = File(baseDir, "cfg").apply { mkdirs() }
            val cfgAgentc = File(cfgDir, "agentc").apply { mkdirs() }
            val stDir = File(baseDir, "st").apply { mkdirs() }
            val dtDir = File(baseDir, "dt").apply { mkdirs() }
            val rtDir = File(baseDir, "rt").apply { mkdirs() }

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
            while (!sock.exists() && System.currentTimeMillis() < deadline) {
                Thread.sleep(50)
            }
            assertTrue("Daemon socket did not appear for $name", sock.exists())

            val pairProc = ProcessBuilder(agentcBin.absolutePath, "pair", "--url", listenUrl)
                .apply { environment().putAll(envMap) }
                .redirectErrorStream(true)
                .start()
            val pairOut = pairProc.inputStream.bufferedReader().readText()
            assertEquals("agentc pair failed for $name: $pairOut", 0, pairProc.waitFor())
            val pairUrl = Regex("http://127\\.0\\.0\\.1:\\d+/p/\\S+").find(pairOut)?.value
                ?: error("Could not find pair URL in: $pairOut")

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
                    put("v", 1)
                    put("purpose", "rpc")
                    put("name", "Robolectric-$name")
                    put("platform", "android")
                    put("app", "0.4.1")
                },
                listener = object : SecureSocket.Listener {
                    override fun onOpen(socket: SecureSocket, reply: JsonObject) {
                        pairedFuture.complete(reply)
                    }
                    override fun onClosed(error: String?) {
                        if (!pairedFuture.isDone) {
                            pairedFuture.completeExceptionally(RuntimeException(error ?: "closed"))
                        }
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
            return LiveDaemonFixture(name, homeDir, rtDir, proc, cfg, devKey)
        }

        private fun stopDaemon(f: LiveDaemonFixture?) {
            if (f == null) return
            runCatching {
                f.process.destroy()
                if (!f.process.waitFor(2, TimeUnit.SECONDS)) {
                    f.process.destroyForcibly()
                    f.process.waitFor(2, TimeUnit.SECONDS)
                }
            }
            val tmuxSock = File(f.rtDir, "agentc/tmux.sock")
            if (tmuxSock.exists()) {
                runCatching {
                    ProcessBuilder("tmux", "-S", tmuxSock.absolutePath, "kill-server")
                        .start()
                        .waitFor(3, TimeUnit.SECONDS)
                }
            }
        }

        @JvmStatic
        @AfterClass
        fun tearDownDaemons() {
            try {
                stopDaemon(hostAFixture)
                stopDaemon(hostBFixture)
                runCatching {
                    http?.dispatcher?.executorService?.shutdown()
                    http?.connectionPool?.evictAll()
                }
            } finally {
                rootTmp?.deleteRecursively()
            }
        }
    }

    @Before
    fun setUp() {
        ExplicitHostChoice.hostId = null
        CurrentHost.id = null
    }

    @After
    fun tearDown() {
        ExplicitHostChoice.hostId = null
        CurrentHost.id = null
    }

    private fun newConnection(f: LiveDaemonFixture): HostConnection =
        HostConnection(
            config = f.config,
            http = http!!,
            deviceName = "RobolectricTest",
            onNotifySeq = { _, _ -> },
            lastNotifSeq = { 0L },
            keyPairProvider = { f.deviceKey },
        )

    @Test
    fun resolveEffectiveHostId_prefersConnectedSecondHostWhenFirstHostDisconnectedUnlessExplicitlyPicked() = runBlocking {
        val fixA = hostAFixture!!
        val fixB = hostBFixture!!
        val connA = newConnection(fixA)
        val connB = newConnection(fixB)
        try {
            // Host A stays offline (Idle), while Host B connects over Noise_IK.
            connB.start()
            withTimeout(10_000) {
                connB.rpc.state.first { it is ConnState.Connected }
            }
            assertFalse("Host A should be disconnected", connA.rpc.isConnected)
            assertTrue("Host B should be connected", connB.rpc.isConnected)

            val settings = AppSettings(
                hosts = listOf(fixA.config, fixB.config),
                activeHostId = fixA.config.id,
            )

            // 1. Default tab resolution falls back to connected Host B when saved Host A is offline.
            assertEquals(
                fixB.config.id,
                resolveEffectiveHostId(
                    explicitRouteHostId = null,
                    settings = settings,
                    conns = listOf(connA, connB),
                ),
            )

            // 2. If user explicitly picks Host A via selectActiveHost / ExplicitHostChoice, respect Host A.
            val app = ApplicationProvider.getApplicationContext<App>()
            app.graph.prefs.update {
                it.copy(hosts = listOf(fixA.config, fixB.config), activeHostId = fixB.config.id)
            }
            selectActiveHost(app.graph.prefs, app.graph.scope, fixA.config.id)
            withTimeout(5_000) {
                app.graph.prefs.flow.first { it.activeHostId == fixA.config.id }
            }
            assertEquals(fixA.config.id, ExplicitHostChoice.hostId)
            assertEquals(
                fixA.config.id,
                resolveEffectiveHostId(
                    explicitRouteHostId = null,
                    settings = app.graph.prefs.value,
                    conns = listOf(connA, connB),
                ),
            )

            // 3. Route-level h=hostB always overrides saved & user-picked host.
            assertEquals(
                fixB.config.id,
                resolveEffectiveHostId(
                    explicitRouteHostId = fixB.config.id,
                    settings = app.graph.prefs.value,
                    conns = listOf(connA, connB),
                ),
            )

            // 4. NewSessionScreen host resolution: defaults & reactively falls back from offline Host A to connected Host B,
            //    while honoring explicit route host or manual user chip selection.
            ExplicitHostChoice.hostId = null
            assertEquals(
                fixB.config.id,
                resolveNewSessionHostId(
                    currentHostId = "",
                    initialHost = "",
                    userChangedHost = false,
                    settings = settings,
                    conns = listOf(connA, connB),
                ),
            )
            // Reactive fallback when initial state had offline Host A before Host B finished connecting
            assertEquals(
                fixB.config.id,
                resolveNewSessionHostId(
                    currentHostId = fixA.config.id,
                    initialHost = "",
                    userChangedHost = false,
                    settings = settings,
                    conns = listOf(connA, connB),
                ),
            )
            // Manual user chip pick keeps offline Host A
            assertEquals(
                fixA.config.id,
                resolveNewSessionHostId(
                    currentHostId = fixA.config.id,
                    initialHost = "",
                    userChangedHost = true,
                    settings = settings,
                    conns = listOf(connA, connB),
                ),
            )
            // Explicit route h=HostA keeps Host A
            assertEquals(
                fixA.config.id,
                resolveNewSessionHostId(
                    currentHostId = fixA.config.id,
                    initialHost = fixA.config.id,
                    userChangedHost = false,
                    settings = settings,
                    conns = listOf(connA, connB),
                ),
            )
        } finally {
            connA.close()
            connB.close()
        }
    }

    @Test
    fun perHostFilesState_isolatesAndRemembersPathsAcrossHostSwitches() {
        val state = PerHostFilesState()

        // Host A starts in /home/alice, navigates into /home/alice/project
        assertEquals("/home/alice", state.pathFor("host-a", home = "/home/alice"))
        state.setPath("host-a", "/home/alice/project")

        // Switch to Host B -> resolves /home/bob without leaking /home/alice/project
        assertEquals("/home/bob", state.pathFor("host-b", home = "/home/bob"))
        state.setPath("host-b", "/home/bob/work")

        // Switch back to Host A -> restores /home/alice/project
        assertEquals("/home/alice/project", state.pathFor("host-a", home = "/home/alice"))
        assertEquals("/home/bob/work", state.pathFor("host-b", home = "/home/bob"))

        // Cold host before hello arrives starts at "~", then upgrades as soon as hello["home"] arrives
        assertEquals("~", state.pathFor("host-c", home = null))
        assertEquals("/home/carol", state.pathFor("host-c", home = "/home/carol"))

        // Bundle-safe PathsByHostSaver round-trips all per-host paths cleanly
        val saved = with(PathsByHostSaver) {
            SaverScope { true }.save(state.snapshot())
        }
        assertNotNull(saved)
        val restored = PathsByHostSaver.restore(saved!!)
        assertEquals(
            mapOf(
                "host-a" to "/home/alice/project",
                "host-b" to "/home/bob/work",
                "host-c" to "/home/carol",
            ),
            restored,
        )
    }

    @Test
    fun hostConfigLabel_disambiguatesLanIpsLocalhostPortsAndCloudflareUrls() {
        val lan1 = HostConfig(id = "1", name = "", url = "http://192.168.1.10:9473", hostKey = "k", devicePub = "p", devicePrivWrapped = "w")
        val lan2 = HostConfig(id = "2", name = "", url = "http://192.168.1.20:9473", hostKey = "k", devicePub = "p", devicePrivWrapped = "w")
        val lo1 = HostConfig(id = "3", name = "", url = "http://127.0.0.1:40001", hostKey = "k", devicePub = "p", devicePrivWrapped = "w")
        val lo2 = HostConfig(id = "4", name = "", url = "http://127.0.0.1:40002", hostKey = "k", devicePub = "p", devicePrivWrapped = "w")
        val cf = HostConfig(id = "5", name = "", url = "https://my-box.trycloudflare.com", hostKey = "k", devicePub = "p", devicePrivWrapped = "w")

        assertEquals("192.168.1.10:9473", lan1.label)
        assertEquals("192.168.1.20:9473", lan2.label)
        assertNotEquals(lan1.label, lan2.label)
        assertNotEquals(lo1.label, lo2.label)
        assertEquals("my-box", cf.label)
    }

    @Test
    fun routes_folderDoesNotCollideWithFilesTabAndEncodesExplicitHost() {
        val standaloneRoute = Routes.files("/home/bob/repo", host = "host-b")
        assertEquals("folder", standaloneRoute.substringBefore('?'))
        assertNotEquals(Routes.FILES, standaloneRoute.substringBefore('?'))
        assertTrue(standaloneRoute.endsWith("&h=host-b"))
        assertEquals("/home/bob/repo", dec(standaloneRoute.substringAfter("path=").substringBefore("&")))

        assertTrue(Routes.file("/home/bob/a.kt", edit = true, host = "host-b").endsWith("&h=host-b"))
        assertTrue(Routes.newSession("/home/bob", "hi", host = "host-b").endsWith("&h=host-b"))
        assertTrue(Routes.term("t1", host = "host-b").endsWith("?h=host-b"))
        assertTrue(Routes.diff("s1", "/home/bob", host = "host-b").endsWith("&h=host-b"))
        assertTrue(Routes.chat("s1", host = "host-b").endsWith("&h=host-b"))
        assertTrue(Routes.artifact("s1", "/tmp/a.md", host = "host-b").endsWith("&h=host-b"))
        assertTrue(Routes.asides("s1", host = "host-b").endsWith("?h=host-b"))

        CurrentHost.id = "host-current"
        assertTrue(Routes.file("/etc/hosts").endsWith("&h=host-current"))
    }

    @Test
    fun navController_roundTripsPlusPercentHexUnicodeAndQueryCharsWithoutDoubleDecodeOrCrash() {
        val app = ApplicationProvider.getApplicationContext<Application>()
        val nav = NavHostController(app)
        nav.navigatorProvider.addNavigator(ComposeNavigator())
        val hArg = navArgument("h") { type = NavType.StringType; defaultValue = "" }
        nav.graph = nav.createGraph(startDestination = Routes.SESSIONS) {
            composable(Routes.SESSIONS) {}
            composable(ROUTE_FOLDER, arguments = listOf(navArgument("path") { type = NavType.StringType; defaultValue = "" }, hArg)) {}
            composable(
                ROUTE_CHAT,
                arguments = listOf(
                    navArgument("sid") { type = NavType.StringType },
                    navArgument("prefill") { type = NavType.StringType; defaultValue = "" },
                    hArg,
                ),
            ) {}
            composable(
                ROUTE_NEW,
                arguments = listOf(
                    navArgument("cwd") { type = NavType.StringType; defaultValue = "" },
                    navArgument("text") { type = NavType.StringType; defaultValue = "" },
                    hArg,
                ),
            ) {}
            composable(
                ROUTE_FILE,
                arguments = listOf(
                    navArgument("path") { type = NavType.StringType; defaultValue = "" },
                    navArgument("edit") { type = NavType.BoolType; defaultValue = false },
                    hArg,
                ),
            ) {}
            composable(
                ROUTE_DIFF,
                arguments = listOf(
                    navArgument("sid") { type = NavType.StringType },
                    navArgument("path") { type = NavType.StringType; defaultValue = "" },
                    hArg,
                ),
            ) {}
            composable(
                ROUTE_ARTIFACT,
                arguments = listOf(
                    navArgument("sid") { type = NavType.StringType },
                    navArgument("path") { type = NavType.StringType; defaultValue = "" },
                    hArg,
                ),
            ) {}
        }

        val trickyPaths = listOf(
            "/home/bob/c++/g++_build+v2.log",
            "/home/bob/100%_coverage.md",
            "/home/bob/literal%41file.txt",
            "/home/bob/My Docs 日本語/q?a=1&b=2#frag+100%_done%41.kt",
        )
        val trickyPrompts = listOf(
            "Fix 50% CPU spike",
            "Investigate c++ g++_build+v2.log & literal%41file.txt?line=10#L20 (日本語 100%_ok)",
        )

        for (p in trickyPaths) {
            nav.navigate(Routes.file(p, edit = true, host = "host-3"))
            assertEquals(p, nav.currentBackStackEntry?.arguments.navString("path"))
            assertEquals(true, nav.currentBackStackEntry?.arguments?.getBoolean("edit"))
            assertEquals("host-3", nav.currentBackStackEntry?.arguments.navString("h"))

            nav.navigate(Routes.files(p, host = "host-3"))
            assertEquals(p, nav.currentBackStackEntry?.arguments.navString("path"))
            assertEquals("host-3", nav.currentBackStackEntry?.arguments.navString("h"))

            nav.navigate(Routes.diff("sid-77", path = p, host = "host-3"))
            assertEquals("sid-77", nav.currentBackStackEntry?.arguments.navString("sid"))
            assertEquals(p, nav.currentBackStackEntry?.arguments.navString("path"))
            assertEquals("host-3", nav.currentBackStackEntry?.arguments.navString("h"))

            nav.navigate(Routes.artifact("sid-77", path = p, host = "host-3"))
            assertEquals("sid-77", nav.currentBackStackEntry?.arguments.navString("sid"))
            assertEquals(p, nav.currentBackStackEntry?.arguments.navString("path"))
            assertEquals("host-3", nav.currentBackStackEntry?.arguments.navString("h"))
        }

        for (prompt in trickyPrompts) {
            val cwd = "/home/bob/c++/100%_dir+v2/%41"
            nav.navigate(Routes.newSession(cwd = cwd, text = prompt, host = "host-3"))
            assertEquals(cwd, nav.currentBackStackEntry?.arguments.navString("cwd"))
            assertEquals(prompt, nav.currentBackStackEntry?.arguments.navString("text"))
            assertEquals("host-3", nav.currentBackStackEntry?.arguments.navString("h"))

            nav.navigate(Routes.chat("sid-88", prefill = prompt, host = "host-3"))
            assertEquals("sid-88", nav.currentBackStackEntry?.arguments.navString("sid"))
            assertEquals(prompt, nav.currentBackStackEntry?.arguments.navString("prefill"))
            assertEquals("host-3", nav.currentBackStackEntry?.arguments.navString("h"))
        }

        // LocalUriHandler path extraction on file:// URIs vs raw /... paths
        val fromFileUri = resolveLocalFileUriPath("file:///home/bob/100%25_done+v2.md#L42")
        assertEquals("/home/bob/100%_done+v2.md", fromFileUri)
        nav.navigate(Routes.file(fromFileUri!!, host = "host-3"))
        assertEquals("/home/bob/100%_done+v2.md", nav.currentBackStackEntry?.arguments.navString("path"))

        val fromRawPath = resolveLocalFileUriPath("/home/bob/100%_done+v2.md#L42")
        assertEquals("/home/bob/100%_done+v2.md", fromRawPath)
        nav.navigate(Routes.file(fromRawPath!!, host = "host-3"))
        assertEquals("/home/bob/100%_done+v2.md", nav.currentBackStackEntry?.arguments.navString("path"))

        assertNull(resolveLocalFileUriPath("https://openrouter.ai/models"))
    }

    @Test
    fun fileTransferToCache_namespacesCacheFilesByHostIdAndIsolatesFsList() = runBlocking {
        val fixA = hostAFixture!!
        val fixB = hostBFixture!!
        val app = ApplicationProvider.getApplicationContext<Application>()

        val repoA = File(fixA.homeDir, "repo").apply { mkdirs() }
        val repoB = File(fixB.homeDir, "repo").apply { mkdirs() }
        val readmeA = File(repoA, "README.md").apply { writeText("Host Alpha unique README") }
        val readmeB = File(repoB, "README.md").apply { writeText("Host Beta unique README") }
        File(fixA.homeDir, "only-on-alpha.txt").writeText("alpha")
        File(fixB.homeDir, "only-on-beta.txt").writeText("beta")

        val connA = newConnection(fixA)
        val connB = newConnection(fixB)
        try {
            connA.start()
            connB.start()
            withTimeout(10_000) {
                connA.hello.first { it != null }
                connB.hello.first { it != null }
            }

            // Verify fs.list on Host A vs Host B isolates each host's home directory
            val listA = connA.rpc.call("fs.list", params { put("path", "~") }).decode(FsList.serializer())
            val listB = connB.rpc.call("fs.list", params { put("path", "~") }).decode(FsList.serializer())
            assertEquals(fixA.homeDir.canonicalPath, File(listA.path).canonicalPath)
            assertEquals(fixB.homeDir.canonicalPath, File(listB.path).canonicalPath)
            assertTrue(listA.entries.any { it.name == "only-on-alpha.txt" })
            assertFalse(listA.entries.any { it.name == "only-on-beta.txt" })
            assertTrue(listB.entries.any { it.name == "only-on-beta.txt" })
            assertFalse(listB.entries.any { it.name == "only-on-alpha.txt" })

            // Verify FileTransfer.toCache namespaces same-named README.md under cacheDir/shared/<hostId>/<pathHash12>/
            val cachedA = FileTransfer.toCache(app, connA, readmeA.absolutePath)
            val cachedB = FileTransfer.toCache(app, connB, readmeB.absolutePath)

            assertNotEquals(cachedA.absolutePath, cachedB.absolutePath)
            assertEquals("README.md", cachedA.name)
            assertEquals(FileTransfer.cachePathHash(readmeA.absolutePath), cachedA.parentFile?.name)
            assertEquals(FileTransfer.sanitizeFileName(connA.config.id), cachedA.parentFile?.parentFile?.name)
            assertEquals(FileTransfer.sanitizeFileName(connB.config.id), cachedB.parentFile?.parentFile?.name)
            assertEquals("shared", cachedA.parentFile?.parentFile?.parentFile?.name)
            assertEquals("Host Alpha unique README", cachedA.readText())
            assertEquals("Host Beta unique README", cachedB.readText())

            // Verify same-host different-directory files (subA/config.json vs subB/config.json) never overwrite each other
            val subA = File(fixA.homeDir, "subA").apply { mkdirs() }
            val subB = File(fixA.homeDir, "subB").apply { mkdirs() }
            val cfgA = File(subA, "config.json").apply { writeText("{\"dir\":\"A\"}") }
            val cfgB = File(subB, "config.json").apply { writeText("{\"dir\":\"B\"}") }

            val cachedCfgA = FileTransfer.toCache(app, connA, cfgA.absolutePath)
            val cachedCfgB = FileTransfer.toCache(app, connA, cfgB.absolutePath)

            assertNotEquals(cachedCfgA.absolutePath, cachedCfgB.absolutePath)
            assertEquals("config.json", cachedCfgA.name)
            assertEquals("config.json", cachedCfgB.name)
            assertEquals("{\"dir\":\"A\"}", cachedCfgA.readText())
            assertEquals("{\"dir\":\"B\"}", cachedCfgB.readText())
            val sharedUriA = FileProvider.getUriForFile(app, "${app.packageName}.files", cachedCfgA)
            assertTrue("FileProvider URI should end with clean leaf filename: $sharedUriA", sharedUriA.toString().endsWith("/config.json"))
        } finally {
            connA.close()
            connB.close()
        }
    }

    @Test
    fun appGraphNotificationSuppression_scopesByHostIdAndSessionId() {
        val app = ApplicationProvider.getApplicationContext<App>()
        val g = app.graph

        val hideA = g.showSession("shared-sid", hostId = "host-a")
        try {
            assertTrue(g.covers("shared-sid", "done", hostId = "host-a"))
            assertTrue(g.covers("shared-sid", "permission", hostId = "host-a"))
            // Identical sessionId on Host B must NOT be suppressed while viewing Host A!
            assertFalse(g.covers("shared-sid", "done", hostId = "host-b"))
            assertFalse(g.covers("shared-sid", "permission", hostId = "host-b"))

            val hideAsideB = g.showSession("shared-sid", only = setOf("aside"), hostId = "host-b")
            try {
                assertTrue(g.covers("shared-sid", "aside", hostId = "host-b"))
                assertFalse(g.covers("shared-sid", "permission", hostId = "host-b"))
                assertFalse(g.covers("shared-sid", "aside", hostId = "host-a"))
            } finally {
                hideAsideB()
            }

            // Disposing Host B's top overlay restores Host A's session coverage.
            assertTrue(g.covers("shared-sid", "done", hostId = "host-a"))
        } finally {
            hideA()
        }
        assertFalse(g.covers("shared-sid", "done", hostId = "host-a"))
    }

    @Test
    fun codexProviderUpsertConfig_preservesRedactedSecretsAndSupportsExplicitClear() {
        val existing = ProviderConfig(
            id = "codex",
            name = "Codex",
            driver = "codex",
            env = mapOf("OPENROUTER_API_KEY" to "***", "CUSTOM_ENV" to "keep-me"),
            defaultModel = "openai/gpt-5",
        )
        val profile = AgentProfile(id = "codex", name = "Codex", driver = "codex", available = true, builtin = true)

        // 1. Blank API key when previously configured preserves "***" and other env keys
        val kept = buildCodexProviderUpsertConfig(
            existing = existing,
            agentProfile = profile,
            typedApiKey = "   ",
            clearStoredKey = false,
            defaultModel = "openai/gpt-5-chat-latest",
            customModels = listOf(CustomModel(slug = "openrouter/google/gemini-2.5-pro:nitro", name = "Gemini Nitro")),
        )
        assertEquals("***", kept.env?.get("OPENROUTER_API_KEY"))
        assertEquals("keep-me", kept.env?.get("CUSTOM_ENV"))
        assertEquals("openai/gpt-5-chat-latest", kept.defaultModel)
        assertEquals("openrouter/google/gemini-2.5-pro:nitro", kept.customModels.single().slug)

        // 2. Blank API key when no prior config sends env = null
        val noPrior = buildCodexProviderUpsertConfig(
            existing = null,
            agentProfile = profile,
            typedApiKey = "",
            clearStoredKey = false,
            defaultModel = "",
            customModels = emptyList(),
        )
        assertNull(noPrior.env)

        // 3. Explicit clear sets OPENROUTER_API_KEY = "" while keeping CUSTOM_ENV
        val cleared = buildCodexProviderUpsertConfig(
            existing = existing,
            agentProfile = profile,
            typedApiKey = "",
            clearStoredKey = true,
            defaultModel = "openai/gpt-5",
            customModels = emptyList(),
        )
        assertEquals("", cleared.env?.get("OPENROUTER_API_KEY"))
        assertEquals("keep-me", cleared.env?.get("CUSTOM_ENV"))
    }
}
