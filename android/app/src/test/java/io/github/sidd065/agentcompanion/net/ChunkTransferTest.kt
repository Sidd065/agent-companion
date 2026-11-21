package io.github.sidd065.agentcompanion.net

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.OkHttpClient
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.io.File
import java.net.ServerSocket
import java.security.MessageDigest
import java.util.Base64
import java.util.Random
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit

class ChunkTransferTest {

    private fun sha256(bytes: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

    @Test
    fun readsMultiChunkBinaryFileWithMonotonicProgress() = runTest {
        val payload = ByteArray(2 * 1024 * 1024 + 345_678)
        Random(42).nextBytes(payload)

        val out = ByteArrayOutputStream()
        val progress = mutableListOf<Pair<Long, Long>>()
        var calls = 0

        val total = ChunkTransfer.readChunks(
            out = out,
            chunkSize = 512L * 1024L,
            onProgress = { d, t -> progress.add(d to t) },
        ) { off, len ->
            calls++
            val start = off.toInt()
            val end = minOf(payload.size, start + len.toInt())
            val slice = payload.copyOfRange(start, end)
            buildJsonObject {
                put("data", Base64.getEncoder().encodeToString(slice))
                put("offset", off)
                put("size", payload.size.toLong())
                put("eof", end >= payload.size)
            }
        }

        assertEquals(payload.size.toLong(), total)
        assertEquals(5, calls)
        assertArrayEquals(payload, out.toByteArray())
        assertEquals(payload.size.toLong(), progress.last().first)
        assertEquals(payload.size.toLong(), progress.last().second)
        for (i in 1 until progress.size) {
            assertTrue("Progress must be monotonic", progress[i].first >= progress[i - 1].first)
        }
    }

    @Test
    fun readsEmptyZeroByteFile() = runTest {
        val out = ByteArrayOutputStream()
        var calls = 0
        val total = ChunkTransfer.readChunks(out = out) { _, _ ->
            calls++
            buildJsonObject {
                put("data", "")
                put("offset", 0L)
                put("size", 0L)
                put("eof", true)
            }
        }
        assertEquals(0L, total)
        assertEquals(1, calls)
        assertEquals(0, out.size())
    }

    @Test
    fun readsExactChunkBoundaryAndStringEof() = runTest {
        val payload = ByteArray(1024 * 1024) { (it % 251).toByte() }
        val out = ByteArrayOutputStream()
        val total = ChunkTransfer.readChunks(out = out, chunkSize = 512L * 1024L) { off, len ->
            val start = off.toInt()
            val end = minOf(payload.size, start + len.toInt())
            buildJsonObject {
                put("data", Base64.getEncoder().encodeToString(payload.copyOfRange(start, end)))
                put("size", payload.size.toLong())
                put("eof", if (end >= payload.size) "true" else "false")
            }
        }
        assertEquals(payload.size.toLong(), total)
        assertArrayEquals(payload, out.toByteArray())
    }

    @Test
    fun propagatesMidStreamErrorCleanly() = runTest {
        val out = ByteArrayOutputStream()
        var calls = 0
        try {
            ChunkTransfer.readChunks(out = out, chunkSize = 1024) { _, _ ->
                calls++
                if (calls == 2) throw RpcException("connection lost")
                buildJsonObject {
                    put("data", Base64.getEncoder().encodeToString(ByteArray(1024)))
                    put("size", 4096L)
                    put("eof", false)
                }
            }
            fail("Expected RpcException")
        } catch (e: RpcException) {
            assertTrue(e.message!!.contains("connection lost"))
            assertEquals(2, calls)
        }
    }

    @Test
    fun stopsImmediatelyOnCancellation() = runTest {
        val out = ByteArrayOutputStream()
        var calls = 0
        val job = launch {
            ChunkTransfer.readChunks(out = out, chunkSize = 1024) { _, _ ->
                calls++
                if (calls == 2) cancel("user cancelled")
                buildJsonObject {
                    put("data", Base64.getEncoder().encodeToString(ByteArray(1024)))
                    put("size", 10_000L)
                    put("eof", false)
                }
            }
        }
        job.join()
        assertTrue(job.isCancelled)
        assertEquals(2, calls)
    }

    /**
     * Real end-to-end integration test: launches the actual Go `agentc` daemon binary,
     * pairs over Noise_IKpsk1 via Kotlin [SecureSocket], connects over Noise_IK, and
     * downloads a 1.7 MiB random binary `.deb` via `fs.download` + [ChunkTransfer.readChunks],
     * verifying byte-for-byte SHA-256 integrity.
     */
    @Test
    fun endToEndKotlinNoiseClientDownloadsBinaryFromRealGoDaemon() = runTest {
        val repoRoot = File(System.getProperty("user.dir")!!).parentFile!!.parentFile!!
        val daemonDir = File(repoRoot, "daemon")
        val home = System.getProperty("user.home")!!
        val goBin = listOf(File(home, ".local/go/bin/go"), File("/usr/local/go/bin/go"), File("/usr/bin/go"))
            .firstOrNull { it.canExecute() }
        if (!daemonDir.isDirectory || goBin == null) {
            // Skip gracefully on CI environments without Go alongside Android checkout.
            return@runTest
        }

        val tmp = java.nio.file.Files.createTempDirectory("agc-kt-dl-").toFile()
        val agentcBin = File(tmp, "agentc")
        val cfgDir = File(tmp, "cfg").apply { mkdirs() }
        val cfgAgentc = File(cfgDir, "agentc").apply { mkdirs() }
        val stDir = File(tmp, "st").apply { mkdirs() }
        val dtDir = File(tmp, "dt").apply { mkdirs() }
        val rtDir = File(tmp, "rt").apply { mkdirs() }
        val sampleFile = File(tmp, "agent-companion-sample.deb")

        val payload = ByteArray(1_750_000)
        Random(1337).nextBytes(payload)
        sampleFile.writeBytes(payload)
        val expectedHash = sha256(payload)

        var daemonProc: Process? = null
        val http = OkHttpClient.Builder()
            .connectTimeout(10, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .build()

        try {
            val build = ProcessBuilder(goBin.absolutePath, "build", "-o", agentcBin.absolutePath, ".")
                .directory(daemonDir)
                .redirectErrorStream(true)
                .start()
            val buildOut = build.inputStream.bufferedReader().readText()
            assertEquals("go build failed: $buildOut", 0, build.waitFor())

            val port = ServerSocket(0).use { it.localPort }
            val listenUrl = "http://127.0.0.1:$port"
            File(cfgAgentc, "config.json").writeText(
                """{"tunnel":"none","listen":"127.0.0.1:$port","publicUrl":"$listenUrl"}""",
            )

            val envMap = mapOf(
                "HOME" to tmp.absolutePath,
                "XDG_CONFIG_HOME" to cfgDir.absolutePath,
                "XDG_STATE_HOME" to stDir.absolutePath,
                "XDG_DATA_HOME" to dtDir.absolutePath,
                "XDG_RUNTIME_DIR" to rtDir.absolutePath,
            )
            daemonProc = ProcessBuilder(agentcBin.absolutePath, "serve", "--listen", "127.0.0.1:$port")
                .apply { environment().putAll(envMap) }
                .redirectErrorStream(true)
                .start()

            // Wait for daemon HTTP endpoint + local socket, then run `agentc pair --url ...` via CLI
            val sock = File(rtDir, "agentc/agentc.sock")
            val deadline = System.currentTimeMillis() + 10_000
            while (!sock.exists() && System.currentTimeMillis() < deadline) {
                Thread.sleep(100)
            }
            assertTrue("Daemon local socket did not appear", sock.exists())

            val pairProc = ProcessBuilder(agentcBin.absolutePath, "pair", "--url", listenUrl)
                .apply { environment().putAll(envMap) }
                .redirectErrorStream(true)
                .start()
            val pairOut = pairProc.inputStream.bufferedReader().readText()
            assertEquals("agentc pair failed: $pairOut", 0, pairProc.waitFor())
            val pairUrl = Regex("""http://127\.0\.0\.1:\d+/p/\S+""").find(pairOut)?.value
                ?: error("Could not find pair URL in: $pairOut")

            val uri = java.net.URI(pairUrl)
            val baseUrl = "${uri.scheme}://${uri.authority}"
            val pairId = uri.path.substringAfter("/p/")
            val frag = uri.rawFragment.split("&").associate {
                val kv = it.split("=", limit = 2)
                kv[0] to java.net.URLDecoder.decode(kv.getOrElse(1) { "" }, "UTF-8")
            }
            val devKey = Noise.generateKeyPair()
            val daemonPub = Base64.getUrlDecoder().decode(frag["k"]!!)
            val psk = Base64.getUrlDecoder().decode(frag["s"]!!)

            // 1. Pair over real Noise_IKpsk1 via Kotlin SecureSocket
            val pairedFuture = CompletableFuture<JsonObject>()
            val pairSock = SecureSocket(
                http = http,
                baseUrl = baseUrl,
                daemonKey = daemonPub,
                deviceKey = devKey,
                hello = buildJsonObject {
                    put("v", 1); put("purpose", "rpc"); put("name", "KotlinE2E"); put("platform", "android"); put("app", "0.4.0")
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
            pairedFuture.get(10, TimeUnit.SECONDS)
            pairSock.close()

            // 2. Connect over real Noise_IK via Kotlin SecureSocket and run chunked fs.download!
            val openFuture = CompletableFuture<JsonObject>()
            val responses = java.util.concurrent.ConcurrentHashMap<Long, CompletableFuture<JsonObject>>()
            val rpcSock = SecureSocket(
                http = http,
                baseUrl = baseUrl,
                daemonKey = daemonPub,
                deviceKey = devKey,
                hello = buildJsonObject {
                    put("v", 1); put("purpose", "rpc"); put("name", "KotlinE2E"); put("platform", "android"); put("app", "0.4.0")
                },
                listener = object : SecureSocket.Listener {
                    override fun onOpen(socket: SecureSocket, reply: JsonObject) { openFuture.complete(reply) }
                    override fun onText(text: String) {
                        val obj = io.github.sidd065.agentcompanion.core.AppJson.parseToJsonElement(text).jsonObject
                        val id = obj["id"]?.jsonPrimitive?.content?.toLongOrNull() ?: return
                        val fut = responses.remove(id) ?: return
                        val err = obj["error"]
                        if (err != null && err.toString() != "null") {
                            fut.completeExceptionally(RpcException(err.toString()))
                        } else {
                            fut.complete(obj["result"]!!.jsonObject)
                        }
                    }
                    override fun onClosed(error: String?) {
                        if (!openFuture.isDone) openFuture.completeExceptionally(RuntimeException(error ?: "closed"))
                    }
                },
            )
            rpcSock.connect()
            openFuture.get(10, TimeUnit.SECONDS)

            var reqId = 0L
            val downloaded = ByteArrayOutputStream()
            val total = ChunkTransfer.readChunks(out = downloaded) { off, len ->
                val id = ++reqId
                val fut = CompletableFuture<JsonObject>()
                responses[id] = fut
                val req = buildJsonObject {
                    put("id", id)
                    put("method", "fs.download")
                    put("params", buildJsonObject {
                        put("path", sampleFile.absolutePath)
                        put("offset", off)
                        put("length", len)
                    })
                }
                assertTrue("sendText failed", rpcSock.sendText(req.toString()))
                fut.get(15, TimeUnit.SECONDS)
            }
            rpcSock.close()

            assertEquals(payload.size.toLong(), total)
            assertEquals(expectedHash, sha256(downloaded.toByteArray()))
        } finally {
            daemonProc?.destroyForcibly()
            http.dispatcher.executorService.shutdown()
            http.connectionPool.evictAll()
            tmp.deleteRecursively()
        }
    }
}
