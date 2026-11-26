package io.github.sidd065.agentcompanion.net

import android.app.Application
import android.os.Looper
import androidx.test.core.app.ApplicationProvider
import com.sun.net.httpserver.HttpServer
import io.github.sidd065.agentcompanion.core.AppJson
import io.github.sidd065.agentcompanion.core.AppSettings
import io.github.sidd065.agentcompanion.core.CodexFlags
import io.github.sidd065.agentcompanion.core.CustomModel
import io.github.sidd065.agentcompanion.core.FsEntry
import io.github.sidd065.agentcompanion.core.FsList
import io.github.sidd065.agentcompanion.core.HostConfig
import io.github.sidd065.agentcompanion.core.ProviderConfig
import io.github.sidd065.agentcompanion.core.ProvidersListResult
import io.github.sidd065.agentcompanion.core.ReadResult
import io.github.sidd065.agentcompanion.core.SearchHit
import io.github.sidd065.agentcompanion.ui.chat.commandOutput
import io.github.sidd065.agentcompanion.ui.chat.diffs
import io.github.sidd065.agentcompanion.ui.chat.toolTitle
import io.github.sidd065.agentcompanion.ui.files.FileTransfer
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.OkHttpClient
import org.junit.AfterClass
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.BeforeClass
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import java.nio.file.Files
import java.io.ByteArrayInputStream
import java.io.File
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.URI
import java.net.URLDecoder
import java.security.MessageDigest
import java.util.Base64
import java.util.Random
import java.util.UUID
import java.util.concurrent.CompletableFuture
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.TimeUnit

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class CodexAndMultiHostNoiseE2ETest {

    data class DaemonHandle(
        val label: String,
        val baseDir: File,
        val homeDir: File,
        val rtDir: File,
        val process: Process,
        val config: HostConfig,
        val deviceKey: Noise.KeyPair,
    )

    companion object {
        private var scratchRoot: File? = null
        private var http: OkHttpClient? = null
        private var mockOrServer: HttpServer? = null
        private val mockOpenRouterChatModels = CopyOnWriteArrayList<String>()
        private var codexRpcLog: File? = null
        private var hostA: DaemonHandle? = null
        private var hostB: DaemonHandle? = null

        @JvmStatic
        @BeforeClass
        fun setUpCluster() {
            val userHome = System.getProperty("user.home")!!
            assertFalse("user.home must not be under /tmp", userHome.startsWith("/tmp"))

            val scratch = File(userHome, ".cache/agc-m3-" + UUID.randomUUID().toString().take(8))
            assertFalse("scratch must never be under /tmp", scratch.absolutePath.startsWith("/tmp"))
            assertTrue(
                "scratch path (${scratch.absolutePath.length} chars) must be < 70 chars for AF_UNIX agentc.sock",
                scratch.absolutePath.length < 70,
            )
            scratch.mkdirs()
            scratchRoot = scratch

            val startDir = File(System.getProperty("user.dir")!!).canonicalFile
            val repoRoot = generateSequence(startDir) { it.parentFile }
                .firstOrNull { File(it, "daemon/go.mod").isFile }
                ?: error("Could not locate daemon/go.mod above $startDir")
            val daemonDir = File(repoRoot, "daemon")
            val goBin = listOf(
                File(userHome, ".local/go/bin/go"),
                File("/usr/local/go/bin/go"),
                File("/usr/bin/go"),
            ).firstOrNull { it.canExecute() } ?: error("Go toolchain not found")

            val agentcBin = File(scratch, "agentc")
            val build = ProcessBuilder(goBin.absolutePath, "build", "-o", agentcBin.absolutePath, ".")
                .directory(daemonDir)
                .redirectErrorStream(true)
                .start()
            val buildOut = build.inputStream.bufferedReader().readText()
            assertEquals("go build ../daemon failed: $buildOut", 0, build.waitFor())

            // Start local mock OpenRouter HTTP server on 127.0.0.1:0
            val orSrv = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
            orSrv.createContext("/api/v1/models") { ex ->
                val body = """
                    {
                      "data": [
                        {
                          "id": "google/gemini-3-flash-preview",
                          "name": "Google: Gemini 3 Flash Preview",
                          "description": "Fast multimodal coding model",
                          "context_length": 1048576,
                          "supported_parameters": ["tools", "reasoning"],
                          "architecture": {"modality": "text+image->text", "input_modalities": ["text", "image"]}
                        },
                        {
                          "id": "qwen/qwen3-coder:exacto",
                          "name": "Qwen3 Coder Exacto",
                          "description": "Coding specialist",
                          "context_length": 262144,
                          "supported_parameters": ["tools"]
                        }
                      ]
                    }
                """.trimIndent().toByteArray(Charsets.UTF_8)
                ex.responseHeaders.add("Content-Type", "application/json")
                ex.sendResponseHeaders(200, body.size.toLong())
                ex.responseBody.use { it.write(body) }
            }
            orSrv.createContext("/api/v1/chat/completions") { ex ->
                val reqRaw = ex.requestBody.bufferedReader(Charsets.UTF_8).readText()
                val reqJson = runCatching { AppJson.parseToJsonElement(reqRaw).jsonObject }.getOrNull()
                val model = reqJson?.get("model")?.jsonPrimitive?.content.orEmpty()
                if (model.isNotEmpty()) {
                    mockOpenRouterChatModels.add(model)
                }
                val reply = buildJsonObject {
                    put("id", "chatcmpl-mock-${mockOpenRouterChatModels.size}")
                    put("model", model)
                    put(
                        "choices",
                        JsonArray(
                            listOf(
                                buildJsonObject {
                                    put(
                                        "message",
                                        buildJsonObject {
                                            put("role", "assistant")
                                            put("content", "Mock OpenRouter response from $model")
                                        },
                                    )
                                    put("finish_reason", "stop")
                                },
                            ),
                        ),
                    )
                }.toString().toByteArray(Charsets.UTF_8)
                ex.responseHeaders.add("Content-Type", "application/json")
                ex.sendResponseHeaders(200, reply.size.toLong())
                ex.responseBody.use { it.write(reply) }
            }
            // Reject any HTTPS CONNECT proxy attempts immediately so daemon never touches external openrouter.ai.
            orSrv.createContext("/") { ex ->
                val msg = """{"error":{"message":"hermetic local proxy block"}}""".toByteArray(Charsets.UTF_8)
                ex.sendResponseHeaders(502, msg.size.toLong())
                ex.responseBody.use { it.write(msg) }
            }
            orSrv.start()
            mockOrServer = orSrv
            val mockOrPort = orSrv.address.port
            val mockOrBaseUrl = "http://127.0.0.1:$mockOrPort/api/v1"

            // Write mock codex 0.172.0 app-server binary & headless no-gio PATH dir (symlinking only safe non-trash tools).
            val noGioBin = File(scratch, "bin").apply { mkdirs() }
            for (sysTool in listOf("/usr/bin/git", "/usr/bin/tmux", "/bin/sh", "/usr/bin/python3")) {
                val src = File(sysTool)
                if (src.canExecute()) {
                    Files.createSymbolicLink(File(noGioBin, src.name).toPath(), src.toPath())
                }
            }
            val rpcLog = File(scratch, "codex-rpc.jsonl")
            codexRpcLog = rpcLog
            val mockCodex = writeMockCodexBinary(scratch)

            val client = OkHttpClient.Builder()
                .connectTimeout(10, TimeUnit.SECONDS)
                .readTimeout(30, TimeUnit.SECONDS)
                .build()
            http = client

            // Pre-populate isolated home directories for Host A and Host B
            val baseA = File(scratch, "a").apply { mkdirs() }
            val homeA = File(baseA, "home").apply { mkdirs() }
            File(homeA, "only-on-a.txt").writeText("alpha-host-only\n")
            File(homeA, "shared-name.txt").writeText("from-host-A")
            val projA = File(homeA, "projA").apply { mkdirs() }
            File(projA, "main.go").writeText("package main\nconst v = 1\n")
            File(projA, "util.go").writeText("oldUtil\n")

            val baseB = File(scratch, "b").apply { mkdirs() }
            val homeB = File(baseB, "home").apply { mkdirs() }
            File(homeB, "only-on-b.txt").writeText("beta-host-only\n")
            File(homeB, "shared-name.txt").writeText("from-host-B")
            val subB = File(homeB, "subB").apply { mkdirs() }
            File(subB, "deep.txt").writeText("deep-hit-on-host-b\n")

            hostA = startAndPairDaemon(
                agentcBin = agentcBin,
                baseDir = baseA,
                homeDir = homeA,
                name = "HostA",
                codexPath = mockCodex.absolutePath,
                openRouterKey = "sk-or-v1-test-dummy-not-secret",
                noGioPath = noGioBin.absolutePath,
                proxyUrl = "http://127.0.0.1:$mockOrPort",
                mockOrUrl = mockOrBaseUrl,
                rpcLogPath = rpcLog.absolutePath,
                client = client,
            )
            val realCodex = File(userHome, ".local/bin/codex")
            assertTrue("Expected real codex binary at ${realCodex.absolutePath}", realCodex.canExecute())

            hostB = startAndPairDaemon(
                agentcBin = agentcBin,
                baseDir = baseB,
                homeDir = homeB,
                name = "HostB",
                codexPath = realCodex.absolutePath,
                openRouterKey = "sk-or-v1-test-dummy-not-secret",
                noGioPath = noGioBin.absolutePath,
                proxyUrl = "http://127.0.0.1:$mockOrPort",
                mockOrUrl = mockOrBaseUrl,
                rpcLogPath = "",
                client = client,
            )
        }

        private fun writeMockCodexBinary(dir: File): File {
            val bin = File(dir, "mock-codex")
            val script = """#!/usr/bin/python3
import json, os, pathlib, sys, urllib.request

argv = sys.argv[1:]
if len(argv) > 0 and argv[0] == "--version":
    print("codex-cli 0.172.0")
    sys.exit(0)

# Reject any leaked agy flags
for forbidden in ("--include-default-customizations", "--disable-telemetry"):
    if forbidden in argv:
        sys.stderr.write(f"FORBIDDEN_AGY_FLAG:{forbidden}\n")
        sys.exit(3)

# Validate -c model_catalog_json=<path> outside /tmp just like codex 0.172.0
for idx, tok in enumerate(argv):
    if tok == "-c" and idx + 1 < len(argv) and argv[idx + 1].startswith("model_catalog_json="):
        raw_val = argv[idx + 1].split("=", 1)[1].strip()
        if raw_val.startswith('"') and raw_val.endswith('"'):
            raw_val = json.loads(raw_val)
        if raw_val.startswith("/tmp/") or raw_val == "/tmp":
            sys.stderr.write(f"CATALOG_UNDER_TMP:{raw_val}\n")
            sys.exit(4)
        cp = pathlib.Path(raw_val)
        if not cp.is_file() or cp.stat().st_size == 0:
            sys.stderr.write("Error: missing model_catalog_json\n")
            sys.exit(2)
        parsed = json.loads(cp.read_text(encoding="utf-8"))
        if not isinstance(parsed.get("models"), list) or len(parsed["models"]) == 0:
            sys.stderr.write("Error: empty model_catalog_json\n")
            sys.exit(2)

log_path = os.environ.get("FAKE_CODEX_LOG", "")
def log_ev(obj):
    if log_path:
        with open(log_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(obj) + "\n")

log_ev({"event": "spawn", "argv": argv})

codex_home = os.environ.get("CODEX_HOME", "")
state_file = pathlib.Path(codex_home) / "mock_threads.json"
mock_or_url = os.environ.get("MOCK_OPENROUTER_URL", "")

def load_state():
    if state_file.exists():
        try:
            return json.loads(state_file.read_text(encoding="utf-8"))
        except Exception:
            pass
    return {"seq": 0, "threads": {}}

def save_state(st):
    state_file.parent.mkdir(parents=True, exist_ok=True)
    state_file.write_text(json.dumps(st), encoding="utf-8")

def write_rollout(th):
    rdir = pathlib.Path(codex_home) / "sessions" / "2026" / "11" / "25"
    rdir.mkdir(parents=True, exist_ok=True)
    rp = rdir / f"rollout-2026-11-25T06-00-00-{th['id']}.jsonl"
    lines = [
        {"timestamp": "2026-11-25T06:00:00Z", "type": "session_meta", "payload": {
            "id": th["id"],
            "timestamp": "2026-11-25T06:00:00Z",
            "cwd": th.get("cwd", ""),
            "model_provider": "openrouter",
        }},
    ]
    for turn in th.get("turns", []):
        lines.append({"timestamp": "2026-11-25T06:00:01Z", "type": "event_msg", "payload": {
            "type": "user_message",
            "message": turn.get("prompt", ""),
        }})
    rp.write_text("\n".join(json.dumps(x) for x in lines) + "\n", encoding="utf-8")
    th["path"] = str(rp)

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    msg = json.loads(raw)
    log_ev(msg)
    rid = msg.get("id")
    method = msg.get("method", "")
    params = msg.get("params") or {}
    if rid is not None and method == "":
        continue

    st = load_state()
    if method == "initialize":
        send({"id": rid, "result": {"userAgent": "codex/0.172.0", "codexHome": codex_home}})
    elif method == "initialized":
        pass
    elif method == "model/list":
        send({"id": rid, "result": {"data": [{"id": "gpt-5.3-codex", "displayName": "GPT-5.3 Codex", "isDefault": True}]}})
    elif method == "thread/start":
        st["seq"] += 1
        tid = f"019c0000-0000-7000-8000-{st['seq']:012d}"
        th = {
            "id": tid,
            "cwd": params.get("cwd", ""),
            "model": params.get("model", "google/gemini-3-flash-preview"),
            "createdAt": 1700000000 + st["seq"],
            "updatedAt": 1700000000 + st["seq"],
            "turns": [],
        }
        write_rollout(th)
        st["threads"][tid] = th
        save_state(st)
        send({"id": rid, "result": {"thread": th, "model": th["model"]}})
    elif method == "thread/resume":
        tid = params.get("threadId")
        th = st["threads"].get(tid, {"id": tid, "cwd": params.get("cwd", ""), "model": params.get("model", "google/gemini-3-flash-preview"), "turns": []})
        if params.get("model"):
            th["model"] = params["model"]
        st["threads"][tid] = th
        save_state(st)
        resp_th = dict(th)
        if params.get("excludeTurns"):
            resp_th["turns"] = []
        send({"id": rid, "result": {"thread": resp_th, "model": th["model"]}})
    elif method == "thread/read":
        tid = params.get("threadId")
        th = st["threads"].get(tid)
        if not th:
            send({"id": rid, "error": {"code": -32600, "message": f"missing thread {tid}"}})
        else:
            send({"id": rid, "result": {"thread": th}})
    elif method == "thread/fork":
        src_id = params.get("threadId")
        src = st["threads"].get(src_id, {"cwd": params.get("cwd", ""), "model": "google/gemini-3-flash-preview", "turns": []})
        st["seq"] += 1
        new_id = f"019c0000-0000-7000-8000-{st['seq']:012d}"
        forked = {
            "id": new_id,
            "forkedFromId": src_id,
            "cwd": params.get("cwd") or src.get("cwd", ""),
            "model": src.get("model", "google/gemini-3-flash-preview"),
            "createdAt": 1700000100 + st["seq"],
            "updatedAt": 1700000100 + st["seq"],
            "turns": list(src.get("turns", [])),
        }
        write_rollout(forked)
        st["threads"][new_id] = forked
        save_state(st)
        send({"id": rid, "result": {"thread": forked, "model": forked["model"]}})
    elif method == "turn/start":
        tid = params.get("threadId")
        th = st["threads"].setdefault(tid, {"id": tid, "cwd": params.get("cwd", ""), "turns": []})
        turn_idx = len(th["turns"]) + 1
        turn_id = f"turn_{turn_idx}"
        model = params.get("model") or th.get("model") or "google/gemini-3-flash-preview"
        th["model"] = model
        prompt_text = ""
        for inp in params.get("input") or []:
            if inp.get("type") == "text":
                prompt_text += inp.get("text", "")
        cwd = params.get("cwd") or th.get("cwd") or ""

        reply_text = f"Ack from {model}"
        if mock_or_url:
            req_data = json.dumps({
                "model": model,
                "messages": [{"role": "user", "content": prompt_text}],
            }).encode("utf-8")
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            http_req = urllib.request.Request(
                mock_or_url + "/chat/completions",
                data=req_data,
                headers={"Content-Type": "application/json"},
                method="POST",
            )
            with opener.open(http_req, timeout=5) as resp:
                body = json.loads(resp.read().decode("utf-8"))
                reply_text = body["choices"][0]["message"]["content"]

        turn_rec = {
            "id": turn_id,
            "prompt": prompt_text,
            "status": "completed",
            "items": [
                {"id": f"u_{turn_idx}", "type": "userMessage", "content": [{"type": "text", "text": prompt_text}]},
                {"id": f"msg_{turn_idx}", "type": "agentMessage", "text": reply_text},
            ],
        }
        th["turns"].append(turn_rec)
        write_rollout(th)
        st["threads"][tid] = th
        save_state(st)

        send({"id": rid, "result": {"turn": {"id": turn_id, "status": "inProgress"}}})
        send({"method": "turn/started", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "inProgress"}}})

        if turn_idx == 1:
            changes = [
                {
                    "path": f"{cwd}/main.go",
                    "kind": {"type": "update"},
                    "diff": "--- a/main.go\n+++ b/main.go\n@@ -1,2 +1,2 @@\n package main\n-const v = 1\n+const v = 2\n",
                },
                {
                    "path": f"{cwd}/util.go",
                    "kind": {"type": "update"},
                    "diff": "--- a/util.go\n+++ b/util.go\n@@ -1 +1 @@\n-oldUtil\n+newUtil\n",
                },
            ]
            send({"method": "item/started", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"edit_{turn_idx}", "type": "fileChange", "status": "inProgress", "changes": changes}}})
            send({"method": "item/completed", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"edit_{turn_idx}", "type": "fileChange", "status": "completed", "changes": changes}}})

            send({"method": "item/started", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"cmd_{turn_idx}", "type": "commandExecution", "command": "go test ./...", "cwd": cwd, "status": "inProgress"}}})
            send({"method": "item/completed", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"cmd_{turn_idx}", "type": "commandExecution", "command": "go test ./...", "cwd": cwd, "status": "completed", "aggregatedOutput": "ok\texample/app\t0.011s\n", "exitCode": 0, "durationMs": 11}}})

        send({"method": "item/started", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"msg_{turn_idx}", "type": "agentMessage", "text": ""}}})
        send({"method": "item/agentMessage/delta", "params": {"threadId": tid, "turnId": turn_id, "itemId": f"msg_{turn_idx}", "delta": reply_text}})
        send({"method": "item/completed", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"msg_{turn_idx}", "type": "agentMessage", "text": reply_text}}})
        send({"method": "turn/completed", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "completed"}}})
    else:
        send({"id": rid, "result": {}})
"""
            bin.writeText(script)
            bin.setExecutable(true, true)
            return bin
        }

        private fun startAndPairDaemon(
            agentcBin: File,
            baseDir: File,
            homeDir: File,
            name: String,
            codexPath: String,
            openRouterKey: String?,
            noGioPath: String,
            proxyUrl: String,
            mockOrUrl: String,
            rpcLogPath: String,
            client: OkHttpClient,
        ): DaemonHandle {
            val cfgDir = File(baseDir, "cfg").apply { mkdirs() }
            val cfgAgentc = File(cfgDir, "agentc").apply { mkdirs() }
            val stDir = File(baseDir, "st").apply { mkdirs() }
            val dtDir = File(baseDir, "dt").apply { mkdirs() }
            val rtDir = File(baseDir, "rt").apply { mkdirs() }

            val port = ServerSocket(0).use { it.localPort }
            val listenUrl = "http://127.0.0.1:$port"
            File(cfgAgentc, "config.json").writeText(
                buildJsonObject {
                    put("tunnel", "none")
                    put("listen", "127.0.0.1:$port")
                    put("publicUrl", listenUrl)
                }.toString(),
            )

            val envMap = mutableMapOf(
                "LANG" to "C.UTF-8",
                "HOME" to homeDir.absolutePath,
                "XDG_CONFIG_HOME" to cfgDir.absolutePath,
                "XDG_STATE_HOME" to stDir.absolutePath,
                "XDG_DATA_HOME" to dtDir.absolutePath,
                "XDG_RUNTIME_DIR" to rtDir.absolutePath,
                "PATH" to noGioPath,
                "HTTPS_PROXY" to proxyUrl,
                "NO_PROXY" to "127.0.0.1,localhost",
                "MOCK_OPENROUTER_URL" to mockOrUrl,
            )
            if (rpcLogPath.isNotEmpty()) {
                envMap["FAKE_CODEX_LOG"] = rpcLogPath
            }

            val cmd = mutableListOf(
                agentcBin.absolutePath,
                "serve",
                "--listen",
                "127.0.0.1:$port",
                "--codex-path",
                codexPath,
            )
            if (!openRouterKey.isNullOrBlank()) {
                cmd += listOf("--openrouter-api-key", openRouterKey)
            }

            val proc = ProcessBuilder(cmd)
                .apply {
                    environment().clear()
                    environment().putAll(envMap)
                }
                .redirectErrorStream(true)
                .start()

            val sock = File(rtDir, "agentc/agentc.sock")
            assertTrue("Socket path too long: ${sock.absolutePath}", sock.absolutePath.length < 104)
            val deadline = System.currentTimeMillis() + 10_000
            while (!sock.exists() && System.currentTimeMillis() < deadline) {
                Thread.sleep(40)
            }
            assertTrue("Daemon socket did not appear for $name", sock.exists())

            val pairProc = ProcessBuilder(agentcBin.absolutePath, "pair", "--url", listenUrl)
                .apply {
                    environment().clear()
                    environment().putAll(envMap)
                }
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
                    put("name", "M3E2E-$name")
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
                devicePrivWrapped = "wrapped-$name",
                deviceId = reply["deviceId"]?.jsonPrimitive?.content.orEmpty(),
            )
            return DaemonHandle(name, baseDir, homeDir, rtDir, proc, cfg, devKey)
        }

        private fun stopDaemon(h: DaemonHandle?) {
            if (h == null) return
            runCatching {
                h.process.destroy()
                if (!h.process.waitFor(2, TimeUnit.SECONDS)) {
                    h.process.destroyForcibly()
                    h.process.waitFor(2, TimeUnit.SECONDS)
                }
            }
            val tmuxSock = File(h.rtDir, "agentc/tmux.sock")
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
        fun tearDownCluster() {
            try {
                stopDaemon(hostA)
                stopDaemon(hostB)
                runCatching { mockOrServer?.stop(0) }
                runCatching {
                    http?.dispatcher?.executorService?.shutdown()
                    http?.connectionPool?.evictAll()
                }
            } finally {
                scratchRoot?.deleteRecursively()
            }
        }
    }

    private fun openHostConnection(
        h: DaemonHandle,
        flagsProvider: (String) -> JsonObject? = { null },
    ): HostConnection = HostConnection(
        config = h.config,
        http = http!!,
        deviceName = "M3Pass3Client",
        onNotifySeq = { _, _ -> },
        lastNotifSeq = { 0L },
        flagsProvider = flagsProvider,
        keyPairProvider = { h.deviceKey },
    )

    @Test
    fun codexOpenRouterSessionFlowOverRealNoiseIk() = runBlocking {
        val hA = hostA!!
        val settings = AppSettings(
            codexFlags = CodexFlags(
                model = "deepseek/deepseek-r1:free",
                mode = "default",
                effort = "high",
            ),
        )
        val connA = openHostConnection(hA, flagsProvider = { agent -> settings.flagsJson(agent) })
        try {
            connA.start()
            withTimeout(10_000) {
                connA.rpc.state.first { it is ConnState.Connected }
                connA.hello.first { it != null }
            }

            // 1. hello / daemon.info openRouterConfigured == true & "codex" profile features
            assertTrue("Expected connA.openRouterConfigured == true", connA.openRouterConfigured)
            val daemonInfo = connA.rpc.call("daemon.info").jsonObject
            assertEquals(true, daemonInfo["openRouterConfigured"]?.jsonPrimitive?.booleanOrNull)
            val codexProfile = connA.agents.value.firstOrNull { it.id == "codex" }
            assertNotNull("Expected codex profile in hello.agents", codexProfile)
            assertTrue(codexProfile!!.available)
            for (feat in listOf("openrouter", "customModels", "thinking", "diffs", "permissions", "fork", "rewind", "steer", "effort", "modes")) {
                assertTrue("Missing feature $feat on codex", connA.agentHasFeature("codex", feat))
            }

            // 2. conn.fetchProviderModels("codex") dynamic catalog, availableModes, reasoningEfforts
            val catalog = connA.fetchProviderModels("codex", refresh = true)
            assertTrue(catalog.openRouterConfigured)
            assertTrue(catalog.models.any { it.modelId == "google/gemini-3-flash-preview" })
            assertTrue(catalog.availableModes.map { it.id }.containsAll(listOf("default", "plan")))
            assertTrue(catalog.reasoningEfforts.containsAll(listOf("low", "medium", "high", "xhigh")))

            // 3. Seed provider env secret, verify "***" redaction, then saveProviderCustomModels preserving "***"
            val seededCfg = ProviderConfig(
                id = "codex",
                name = "Codex",
                driver = "codex",
                enabled = true,
                env = mapOf("OPENROUTER_API_KEY" to "sk-or-v1-stored-dummy-key"),
                builtin = true,
            )
            connA.rpc.call("providers.upsert", AppJson.encodeToJsonElement(ProviderConfig.serializer(), seededCfg).jsonObject)
            val beforeList = connA.rpc.call("providers.list").decode(ProvidersListResult.serializer())
            assertEquals("***", beforeList.configs.first { it.id == "codex" }.env?.get("OPENROUTER_API_KEY"))

            val updatedCatalog = connA.saveProviderCustomModels(
                providerId = "codex",
                customModels = listOf(CustomModel("deepseek/deepseek-r1:free", "DeepSeek R1 Free")),
            )
            assertTrue(updatedCatalog.customModels.any { it.slug == "deepseek/deepseek-r1:free" && it.name == "DeepSeek R1 Free" })
            assertTrue(updatedCatalog.models.any { it.modelId == "deepseek/deepseek-r1:free" && it.isCustom })

            val afterList = connA.rpc.call("providers.list").decode(ProvidersListResult.serializer())
            assertEquals(
                "saveProviderCustomModels must preserve redacted '***' OPENROUTER_API_KEY",
                "***",
                afterList.configs.first { it.id == "codex" }.env?.get("OPENROUTER_API_KEY"),
            )
            assertEquals(
                true,
                connA.rpc.call("daemon.info").jsonObject["openRouterConfigured"]?.jsonPrimitive?.booleanOrNull,
            )

            // 4. session.create(agent = "codex", flags = CodexFlags(...).daemonJson()) sending zero agyFlags
            val flagsObj = settings.codexFlags.daemonJson()
            assertFalse(flagsObj.containsKey("includeDefaultCustomizations"))
            assertFalse(flagsObj.containsKey("disableTelemetry"))
            assertEquals(false, flagsObj["skipPermissions"]?.jsonPrimitive?.booleanOrNull)

            val projA = File(hA.homeDir, "projA")
            mockOpenRouterChatModels.clear()
            val createRes = connA.rpc.call(
                "session.create",
                params {
                    put("agent", "codex")
                    put("cwd", projA.absolutePath)
                    put("flags", flagsObj)
                    put(
                        "prompt",
                        params {
                            put("text", "Implement v2 and run go test")
                        },
                    )
                },
                timeoutMs = 30_000,
            ).jsonObject
            val sessionId = createRes["session"]!!.jsonObject["id"]!!.jsonPrimitive.content

            val store = connA.store(sessionId)
            store.attach()
            try {
                // Wait for Turn 1 items (Edit diff + Bash exitCode + agent message) and idle state
                withTimeout(15_000) {
                    while (
                        store.summary.value?.state != "idle" ||
                        store.items.none { it.kind == "agent" && it.text?.contains("deepseek/deepseek-r1:free") == true } ||
                        store.items.none { it.tool?.kind == "edit" && it.tool.status == "completed" } ||
                        store.items.none { it.tool?.kind == "execute" && it.tool.status == "completed" }
                    ) {
                        Shadows.shadowOf(Looper.getMainLooper()).idle()
                        delay(25)
                    }
                }

                assertTrue(
                    "Mock OpenRouter HTTP server should receive deepseek/deepseek-r1:free",
                    mockOpenRouterChatModels.contains("deepseek/deepseek-r1:free"),
                )

                val editTool = store.items.first { it.tool?.kind == "edit" && it.tool.status == "completed" }.tool!!
                assertEquals(2, editTool.diffs().size)
                assertEquals("main.go (+1 more)", toolTitle(editTool))

                val bashTool = store.items.first { it.tool?.kind == "execute" && it.tool.status == "completed" }.tool!!
                val (exitCode, cmdOut) = bashTool.commandOutput()
                assertEquals(0, exitCode)
                assertEquals("ok\texample/app\t0.011s", cmdOut)

                // Verify codex app-server received zero agy flags and exact model/effort/sandbox/approvalPolicy
                val rpcLines = codexRpcLog!!.readLines().filter { it.isNotBlank() }
                assertTrue(rpcLines.any { it.contains("\"method\": \"turn/start\"") && it.contains("deepseek/deepseek-r1:free") && it.contains("\"effort\": \"high\"") })
                assertFalse(rpcLines.any { it.contains("--include-default-customizations") || it.contains("--disable-telemetry") })

                // Switch to unlisted "openai/gpt-5-chat-latest" via session.setModel with -latest suffix intact
                connA.rpc.call(
                    "session.setModel",
                    params {
                        put("id", sessionId)
                        put("model", "openai/gpt-5-chat-latest")
                    },
                )
                connA.rpc.call(
                    "session.prompt",
                    params {
                        put("id", sessionId)
                        put("text", "Run turn 2 on latest model")
                    },
                )
                withTimeout(15_000) {
                    while (
                        store.summary.value?.state != "idle" ||
                        store.items.none { it.kind == "agent" && it.text?.contains("openai/gpt-5-chat-latest") == true }
                    ) {
                        Shadows.shadowOf(Looper.getMainLooper()).idle()
                        delay(25)
                    }
                }
                assertEquals("openai/gpt-5-chat-latest", store.summary.value?.model)
                assertTrue(
                    "Expected -latest suffix preserved end-to-end to OpenRouter upstream",
                    mockOpenRouterChatModels.contains("openai/gpt-5-chat-latest"),
                )

                // Exercise session.fork on Codex session
                val forkRes = connA.rpc.call("session.fork", params { put("id", sessionId) }, timeoutMs = 30_000).jsonObject
                val forkId = forkRes["session"]!!.jsonObject["id"]!!.jsonPrimitive.content
                assertNotEquals(sessionId, forkId)

                val forkStore = connA.store(forkId)
                forkStore.attach()
                try {
                    withTimeout(10_000) {
                        while (!forkStore.loaded.value) {
                            Shadows.shadowOf(Looper.getMainLooper()).idle()
                            delay(25)
                        }
                    }
                    assertEquals("codex", forkStore.summary.value?.agent)
                    assertEquals(sessionId, forkStore.summary.value?.forkedFrom)
                    assertTrue("Forked Codex session should clone prior transcript items", forkStore.items.isNotEmpty())
                } finally {
                    forkStore.detach()
                }
            } finally {
                store.detach()
                Shadows.shadowOf(Looper.getMainLooper()).idle()
            }
        } finally {
            connA.close()
            Shadows.shadowOf(Looper.getMainLooper()).idle()
        }
    }

    @Test
    fun twoSimultaneousDaemonsMultiHostFsAndTransferIsolationOverNoiseIk() = runBlocking {
        val hA = hostA!!
        val hB = hostB!!
        val ctx = ApplicationProvider.getApplicationContext<Application>()
        val connA = openHostConnection(hA)
        val connB = openHostConnection(hB)
        try {
            connA.start()
            connB.start()
            withTimeout(10_000) {
                connA.rpc.state.first { it is ConnState.Connected }
                connB.rpc.state.first { it is ConnState.Connected }
                connA.hello.first { it != null }
                connB.hello.first { it != null }
            }

            val homeAPath = hA.homeDir.canonicalPath
            val homeBPath = hB.homeDir.canonicalPath
            assertEquals(homeAPath, connA.hello.value?.get("home")?.jsonPrimitive?.content)
            assertEquals(homeBPath, connB.hello.value?.get("home")?.jsonPrimitive?.content)

            // 1. fs.list("~") isolated per host
            val listA = connA.rpc.call("fs.list", params { put("path", "~") }).decode(FsList.serializer())
            val listB = connB.rpc.call("fs.list", params { put("path", "~") }).decode(FsList.serializer())
            val namesA = listA.entries.map { it.name }.toSet()
            val namesB = listB.entries.map { it.name }.toSet()
            assertTrue(namesA.contains("only-on-a.txt"))
            assertFalse(namesA.contains("only-on-b.txt"))
            assertTrue(namesB.contains("only-on-b.txt"))
            assertTrue(namesB.contains("subB"))
            assertFalse(namesB.contains("only-on-a.txt"))

            // 2. fs.search on Host B returns only Host B hits
            val hitsB = connB.rpc.call(
                "fs.search",
                params {
                    put("root", homeBPath)
                    put("query", "deep")
                    put("max", 20)
                },
            ).decode(ListSerializer(SearchHit.serializer()))
            assertTrue(hitsB.any { it.path?.endsWith("subB/deep.txt") == true })
            assertTrue(hitsB.none { it.path?.startsWith(homeAPath) == true })

            // 3. fs.mkdir, fs.create, fs.write, fs.read, fs.rename, fs.copy on Host B leave Host A untouched
            val nestedDirB = "$homeBPath/work/nested"
            connB.rpc.call("fs.mkdir", params { put("path", nestedDirB); put("parents", true) }).decode(FsEntry.serializer())
            val createdPathB = "$nestedDirB/notes.txt"
            connB.rpc.call("fs.create", params { put("path", createdPathB) }).decode(FsEntry.serializer())
            connB.rpc.call("fs.write", params { put("path", createdPathB); put("text", "written-on-host-b\n") }).decode(FsEntry.serializer())

            val readB = connB.rpc.call("fs.read", params { put("path", createdPathB) }).decode(ReadResult.serializer())
            assertEquals("written-on-host-b\n", readB.text)

            val renamedPathB = "$nestedDirB/renamed.txt"
            connB.rpc.call("fs.rename", params { put("from", createdPathB); put("to", renamedPathB) }).decode(FsEntry.serializer())
            assertFalse(File(createdPathB).exists())
            assertTrue(File(renamedPathB).isFile)

            val copiedPathB = "$homeBPath/work/copied.txt"
            connB.rpc.call("fs.copy", params { put("from", renamedPathB); put("to", copiedPathB) }).decode(FsEntry.serializer())
            assertEquals("written-on-host-b\n", File(copiedPathB).readText())
            assertFalse("Host A must remain untouched by Host B fs.* mutations", File(homeAPath, "work").exists())

            // 4. Chunked connB.upload (1.25 MiB) + FileTransfer.toCache non-collision across Host A and Host B
            val binaryPayload = ByteArray(1_250_000)
            Random(20261125L).nextBytes(binaryPayload)
            val expectedSha = sha256Hex(binaryPayload)
            val uploadedPathB = "$homeBPath/uploaded.bin"
            connB.upload(uploadedPathB, ByteArrayInputStream(binaryPayload), overwrite = true)
            assertEquals(expectedSha, sha256Hex(File(uploadedPathB).readBytes()))
            assertFalse(File(homeAPath, "uploaded.bin").exists())

            val cachedA = FileTransfer.toCache(ctx, connA, "$homeAPath/shared-name.txt")
            val cachedB = FileTransfer.toCache(ctx, connB, "$homeBPath/shared-name.txt")
            assertNotEquals("Per-host cache paths must not collide", cachedA.absolutePath, cachedB.absolutePath)
            assertEquals("from-host-A", cachedA.readText())
            assertEquals("from-host-B", cachedB.readText())

            // 5. fs.delete without permanent fails with TRASH_UNSUPPORTED: under headless no-gio PATH, then permanent = true deletes only on Host B
            val nonPermRes = runCatching {
                connB.rpc.call(
                    "fs.delete",
                    params {
                        put("paths", JsonArray(listOf(JsonPrimitive("$homeBPath/shared-name.txt"))))
                        put("permanent", false)
                    },
                )
            }
            assertTrue("Expected fs.delete(permanent=false) to fail without gio", nonPermRes.isFailure)
            val errMsg = nonPermRes.exceptionOrNull()?.message.orEmpty()
            assertTrue("Expected TRASH_UNSUPPORTED: prefix, got: $errMsg", errMsg.contains("TRASH_UNSUPPORTED:"))
            assertTrue("Host B file must remain intact before permanent confirmation", File(homeBPath, "shared-name.txt").isFile)

            connB.rpc.call(
                "fs.delete",
                params {
                    put("paths", JsonArray(listOf(JsonPrimitive("$homeBPath/shared-name.txt"))))
                    put("permanent", true)
                },
            )
            assertFalse("Host B file must be deleted on permanent=true", File(homeBPath, "shared-name.txt").exists())
            assertEquals(
                "Host A shared-name.txt must remain completely untouched",
                "from-host-A",
                File(homeAPath, "shared-name.txt").readText(),
            )
        } finally {
            connA.close()
            connB.close()
        }
    }

    @Test
    fun realCodex0172TerminalCreateOverNoiseIkHonorsUntrustedAndOpenAiProviderWithoutCrash() = runBlocking {
        val hB = hostB!!
        val connB = openHostConnection(hB)
        var createdTermId: String? = null
        try {
            connB.start()
            withTimeout(10_000) {
                connB.rpc.state.first { it is ConnState.Connected }
                connB.hello.first { it != null }
            }
            assertTrue("HostB should have OpenRouter configured while honoring explicit provider=openai", connB.openRouterConfigured)

            val flags = CodexFlags(
                approvalPolicy = "untrusted",
                sandbox = "read-only",
                provider = "openai",
                model = "gpt-4.1-mini",
            )
            val created = connB.rpc.call(
                "term.create",
                params {
                    put("kind", "codex")
                    put("cwd", hB.homeDir.absolutePath)
                    put("skipPermissions", flags.effectiveSkipPermissions)
                    put("args", JsonArray(flags.tuiArgs().map { JsonPrimitive(it) }))
                },
            ).jsonObject
            val termId = created["id"]!!.jsonPrimitive.content
            createdTermId = termId

            // Wait 1.5s to ensure real codex-cli 0.172.0 does NOT exit immediately with config rejection.
            delay(1500)

            val listRes = connB.rpc.call("term.list")
            val listArr = (listRes as? JsonArray) ?: listRes.jsonObject["terms"]!!.jsonArray
            assertTrue(
                "Expected Codex terminal $termId to remain alive in term.list, got: $listArr",
                listArr.any { it.jsonObject["id"]?.jsonPrimitive?.content == termId },
            )

            val tmuxSock = File(hB.rtDir, "agentc/tmux.sock")
            val paneProc = ProcessBuilder(
                "tmux", "-S", tmuxSock.absolutePath,
                "list-panes", "-t", termId, "-F", "#{pane_dead}::#{pane_start_command}",
            ).redirectErrorStream(true).start()
            val paneInfo = paneProc.inputStream.bufferedReader().readText().trim()
            assertEquals("tmux list-panes failed: $paneInfo", 0, paneProc.waitFor())
            assertTrue("Expected live pane (pane_dead=0), got: $paneInfo", paneInfo.startsWith("0::"))
            assertFalse("Must never emit removed approval_policy=untrusted: $paneInfo", paneInfo.contains("untrusted"))
            assertTrue("Expected -a on-request in pane command: $paneInfo", paneInfo.contains("on-request"))
            assertFalse(
                "Must not inject OpenRouter config overrides when explicit provider=openai: $paneInfo",
                paneInfo.contains("openrouter"),
            )
            assertTrue(
                "Expected model_provider=openai in pane command: $paneInfo",
                paneInfo.contains("openai"),
            )
        } finally {
            createdTermId?.let { tid ->
                runCatching { connB.rpc.call("term.kill", params { put("id", tid) }) }
            }
            connB.close()
        }
    }

    private fun sha256Hex(bytes: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
}
