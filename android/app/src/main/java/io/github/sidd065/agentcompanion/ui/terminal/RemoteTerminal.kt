package io.github.sidd065.agentcompanion.ui.terminal

import android.content.ClipboardManager
import android.content.Context
import android.util.Log
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.inputmethod.InputMethodManager
import com.termux.terminal.KeyHandler
import com.termux.terminal.TerminalSession
import com.termux.terminal.TerminalSessionClient
import com.termux.view.TerminalView
import com.termux.view.TerminalViewClient
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.ui.components.copyText
import io.github.sidd065.agentcompanion.net.SecureSocket
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/** Bridges a Termux TerminalSession to a tmux attachment over an encrypted connection. */
class RemoteTerminal(private val ctx: Context, private val conn: HostConnection, val termId: String) : TerminalSession.RemoteIO, TerminalSessionClient {
    val session = TerminalSession(4000, this, this)
    val state = MutableStateFlow("connecting") // connecting|connected|closed|error
    /** The program in the terminal ended (not just our connection). */
    val exited = MutableStateFlow(false)
    val title = MutableStateFlow<String?>(null)
    var error: String? = null
    var view: TerminalView? = null
    @Volatile private var ws: SecureSocket? = null
    private var cols = 0
    private var rows = 0
    private var started = false

    private fun connect() {
        if (started) return
        started = true
        val c = if (cols > 0) cols else 80
        val r = if (rows > 0) rows else 24
        val hello = buildJsonObject { put("purpose", "term"); put("term", termId); put("cols", c); put("rows", r) }
        ws = conn.openSocket(hello, object : SecureSocket.Listener {
            override fun onOpen(socket: SecureSocket, reply: JsonObject) {
                state.value = "connected"
                // The daemon attaches after our first message.
                socket.sendText("""{"type":"resize","cols":$c,"rows":$r}""")
                if (cols > 0 && (cols != c || rows != r)) socket.sendText("""{"type":"resize","cols":$cols,"rows":$rows}""")
            }

            override fun onBinary(bytes: ByteArray) {
                session.feed(bytes, 0, bytes.size)
            }

            override fun onText(text: String) {
                if (text.contains("\"exit\"")) {
                    exited.value = true
                    state.value = "closed"
                    session.remoteExited(0)
                } else if (text.contains("\"error\"")) {
                    error = Regex("\"message\":\"([^\"]*)\"").find(text)?.groupValues?.get(1) ?: "terminal error"
                    state.value = "error"
                    session.remoteExited(1)
                }
            }

            override fun onClosed(error: String?) {
                if (error != null && state.value != "closed") {
                    this@RemoteTerminal.error = error
                    state.value = "error"
                    session.remoteExited(1)
                } else {
                    if (state.value != "error") state.value = "closed"
                    session.remoteExited(0)
                }
            }
        })
    }

    // RemoteIO
    override fun send(data: ByteArray, offset: Int, count: Int) {
        ws?.sendBinary(data.copyOfRange(offset, offset + count))
    }

    override fun resize(columns: Int, rows: Int) {
        val changed = columns != cols || rows != this.rows
        cols = columns
        this.rows = rows
        if (!started) connect() else if (changed) ws?.sendText("""{"type":"resize","cols":$columns,"rows":$rows}""")
    }

    override fun close() {
        ws?.close()
        ws = null
    }

    fun sendText(s: String) = session.writeString(s)

    fun sendKey(keyCode: Int, ctrl: Boolean = false, alt: Boolean = false, shift: Boolean = false) {
        val emu = session.emulator ?: return
        var mod = 0
        if (ctrl) mod = mod or KeyHandler.KEYMOD_CTRL
        if (alt) mod = mod or KeyHandler.KEYMOD_ALT
        if (shift) mod = mod or KeyHandler.KEYMOD_SHIFT
        val code = KeyHandler.getCode(keyCode, mod, emu.isCursorKeysApplicationMode, emu.isKeypadApplicationMode) ?: return
        session.writeString(code)
    }

    fun paste() {
        val cm = ctx.getSystemService(ClipboardManager::class.java)
        val text = cm.primaryClip?.getItemAt(0)?.coerceToText(ctx)?.toString() ?: return
        session.emulator?.paste(text) ?: session.writeString(text)
    }

    // TerminalSessionClient
    override fun onTextChanged(changedSession: TerminalSession) { view?.onScreenUpdated() }
    override fun onTitleChanged(changedSession: TerminalSession) { title.value = changedSession.title }
    override fun onSessionFinished(finishedSession: TerminalSession) { if (state.value == "connected") state.value = "closed" }
    override fun onCopyTextToClipboard(session: TerminalSession, text: String) { ctx.copyText(text, "terminal") }
    override fun onPasteTextFromClipboard(session: TerminalSession?) { paste() }
    override fun onBell(session: TerminalSession) {}
    override fun onColorsChanged(session: TerminalSession) { view?.invalidate() }
    override fun onTerminalCursorStateChange(state: Boolean) {}
    override fun getTerminalCursorStyle(): Int = com.termux.terminal.TerminalEmulator.TERMINAL_CURSOR_STYLE_BLOCK
    override fun logError(tag: String?, message: String?) { Log.e(tag ?: "Term", message ?: "") }
    override fun logWarn(tag: String?, message: String?) { Log.w(tag ?: "Term", message ?: "") }
    override fun logInfo(tag: String?, message: String?) {}
    override fun logDebug(tag: String?, message: String?) {}
    override fun logVerbose(tag: String?, message: String?) {}
    override fun logStackTraceWithMessage(tag: String?, message: String?, e: Exception?) { Log.w(tag ?: "Term", message, e) }
    override fun logStackTrace(tag: String?, e: Exception?) { Log.w(tag ?: "Term", e) }
}

/** Modifier state shared with the extra-keys bar. */
class Modifiers {
    val ctrl = MutableStateFlow(false)
    val alt = MutableStateFlow(false)
}

class ViewClient(
    private val ctx: Context,
    private val mods: Modifiers,
    private val onFontScale: (Float) -> Float,
) : TerminalViewClient {
    var view: TerminalView? = null

    override fun onScale(scale: Float): Float = onFontScale(scale)
    override fun onSingleTapUp(e: MotionEvent?) {
        val v = view ?: return
        v.requestFocus()
        ctx.getSystemService(InputMethodManager::class.java)?.showSoftInput(v, 0)
    }
    override fun shouldBackButtonBeMappedToEscape() = false
    override fun shouldEnforceCharBasedInput() = true
    override fun shouldUseCtrlSpaceWorkaround() = false
    override fun isTerminalViewSelected() = true
    override fun copyModeChanged(copyMode: Boolean) {}
    override fun onKeyDown(keyCode: Int, e: KeyEvent?, session: TerminalSession?) = false
    override fun onKeyUp(keyCode: Int, e: KeyEvent?) = false
    override fun onLongPress(event: MotionEvent?) = false
    override fun readControlKey(): Boolean {
        val v = mods.ctrl.value
        if (v) mods.ctrl.value = false
        return v
    }
    override fun readAltKey(): Boolean {
        val v = mods.alt.value
        if (v) mods.alt.value = false
        return v
    }
    override fun readShiftKey() = false
    override fun readFnKey() = false
    override fun onCodePoint(codePoint: Int, ctrlDown: Boolean, session: TerminalSession?) = false
    override fun onEmulatorSet() {}
    override fun logError(tag: String?, message: String?) { Log.e(tag ?: "TermView", message ?: "") }
    override fun logWarn(tag: String?, message: String?) {}
    override fun logInfo(tag: String?, message: String?) {}
    override fun logDebug(tag: String?, message: String?) {}
    override fun logVerbose(tag: String?, message: String?) {}
    override fun logStackTraceWithMessage(tag: String?, message: String?, e: Exception?) {}
    override fun logStackTrace(tag: String?, e: Exception?) {}
}
